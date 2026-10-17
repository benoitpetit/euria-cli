//go:build linux

package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/pbkdf2"
	_ "modernc.org/sqlite"
)

const euriaOrigin = "https://euria.infomaniak.com/"

// ErrNoSession indicates that no selected Chrome/Chromium profile contains Euria cookies.
var ErrNoSession = errors.New("no Euria browser session found")

// ErrNoCookieDatabase indicates that a browser profile has not created its cookie store yet.
var ErrNoCookieDatabase = errors.New("Chrome/Chromium profile has no Cookies database")

// Profile identifies a Chrome or Chromium profile on disk.
type Profile struct {
	Browser string
	Name    string
	Path    string
}

// Session contains Euria cookies loaded from one browser profile.
type Session struct {
	Profile   Profile
	Cookies   []*http.Cookie
	XsrfToken string
}

// CookieSource lists browser profiles and reads cookies for a target URL.
type CookieSource interface {
	Profiles(ctx context.Context, selector string) ([]Profile, error)
	Cookies(ctx context.Context, profile Profile, target *url.URL) ([]*http.Cookie, error)
}

// ChromiumCookieSource reads Chrome and Chromium profile data on Linux.
type ChromiumCookieSource struct {
	homeDir    string
	configHome string
}

// NewChromiumCookieSource creates a source using the current user's Linux profile roots.
func NewChromiumCookieSource() *ChromiumCookieSource {
	home, _ := os.UserHomeDir()
	return &ChromiumCookieSource{homeDir: home, configHome: os.Getenv("XDG_CONFIG_HOME")}
}

type profileRoot struct {
	browser string
	path    string
}

// Profiles finds Chrome and Chromium profile directories or resolves a name or path.
func (s *ChromiumCookieSource) Profiles(ctx context.Context, selector string) ([]Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if selector != "" && isPathSelector(selector) {
		return s.profilesAtPath(selector)
	}

	profiles := s.discoverProfiles()
	if selector == "" {
		return profiles, nil
	}
	var selected []Profile
	for _, profile := range profiles {
		if profile.Name == selector {
			selected = append(selected, profile)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("Chrome/Chromium profile %q was not found; pass a profile name or path", selector)
	}
	return selected, nil
}

// Cookies reads only Euria-origin cookies directly from the profile database.
func (s *ChromiumCookieSource) Cookies(ctx context.Context, profile Profile, target *url.URL) ([]*http.Cookie, error) {
	if target == nil || target.Scheme != "https" || !strings.EqualFold(target.Hostname(), "euria.infomaniak.com") || (target.Port() != "" && target.Port() != "443") {
		return nil, errors.New("browser cookies may only be read for the Euria HTTPS origin")
	}
	dbPath, err := profileCookieDatabase(profile.Path)
	if err != nil {
		return nil, err
	}
	dbURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dbURL)
	if err != nil {
		return nil, errors.New("could not open the selected browser cookie database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.New("could not read the selected browser cookie database; close the browser or select another profile")
	}
	version := chromiumMetaVersion(ctx, db)
	rows, err := db.QueryContext(ctx, `SELECT host_key, name, path, value, encrypted_value, expires_utc, is_secure, is_httponly, samesite
		FROM cookies
		WHERE (host_key IN ('euria.infomaniak.com', '.euria.infomaniak.com', 'infomaniak.com', '.infomaniak.com')
			OR host_key LIKE '%.euria.infomaniak.com' OR host_key LIKE '%.infomaniak.com')
		AND (path = '' OR path = '/')`)
	if err != nil {
		return nil, errors.New("could not read Euria cookies from the selected browser profile")
	}
	defer rows.Close()
	var cookies []*http.Cookie
	var password string
	passwordRead := false
	for rows.Next() {
		var host, name, path, plainValue string
		var encrypted []byte
		var expiry, secure, httpOnly, sameSite sql.NullInt64
		if err := rows.Scan(&host, &name, &path, &plainValue, &encrypted, &expiry, &secure, &httpOnly, &sameSite); err != nil {
			return nil, errors.New("could not read Euria cookies from the selected browser profile")
		}
		if name == "" || !cookieDomainMatches(host, target.Hostname()) {
			continue
		}
		if path == "" {
			path = "/"
		}
		if !cookiePathMatches(target.Path, path) || (expiry.Valid && expiry.Int64 > 0 && chromiumExpiry(expiry.Int64).Before(time.Now())) {
			continue
		}
		value := plainValue
		if value == "" && len(encrypted) > 0 {
			if len(encrypted) >= 3 && string(encrypted[:3]) == "v11" && !passwordRead {
				password, err = linuxSafeStoragePassword(profile.Browser)
				passwordRead = true
			}
			if err != nil {
				return nil, errors.New("could not decrypt Euria cookies; unlock the desktop keyring and retry")
			}
			decrypted, ok := decryptChromiumCookie(encrypted, version, password)
			if !ok {
				return nil, errors.New("could not decrypt Euria cookies; the Chromium keyring or cookie format may be unsupported")
			}
			value = decrypted
		}
		if value == "" {
			continue
		}
		cookie := &http.Cookie{
			Name: name, Value: value, Domain: strings.TrimPrefix(host, "."), Path: path,
			Secure: secure.Valid && secure.Int64 == 1, HttpOnly: httpOnly.Valid && httpOnly.Int64 == 1,
			SameSite: chromiumSameSite(sameSite.Int64),
		}
		if expiry.Valid && expiry.Int64 > 0 {
			cookie.Expires = chromiumExpiry(expiry.Int64)
		}
		cookies = append(cookies, cookie)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("could not read Euria cookies from the selected browser profile")
	}
	return cookies, nil
}

func profileCookieDatabase(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", errors.New("Chrome/Chromium profile path is not readable")
	}
	if !info.IsDir() {
		if filepath.Base(path) != "Cookies" {
			return "", errors.New("Chrome/Chromium profile path must be a profile directory or Cookies database")
		}
		return path, nil
	}
	for _, candidate := range []string{filepath.Join(path, "Network", "Cookies"), filepath.Join(path, "Cookies")} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", ErrNoCookieDatabase
}

func chromiumMetaVersion(ctx context.Context, db *sql.DB) int64 {
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'version'`).Scan(&raw); err != nil {
		return 0
	}
	version, _ := strconv.ParseInt(raw, 10, 64)
	return version
}

func linuxSafeStoragePassword(browser string) (string, error) {
	application := ""
	service := ""
	account := ""
	switch browser {
	case "chrome":
		application, service, account = "chrome", "Chrome Safe Storage", "Chrome"
	case "chromium":
		application, service, account = "chromium", "Chromium Safe Storage", "Chromium"
	default:
		return "", errors.New("unsupported Chromium keyring profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "secret-tool", "lookup", "application", application).Output(); err == nil {
		if password := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r"); password != "" {
			return password, nil
		}
	}
	if password, err := linuxKWalletPassword(ctx, service, account); err == nil && password != "" {
		return password, nil
	}
	if password, err := keyring.Get(service, account); err == nil && password != "" {
		return password, nil
	}
	return "", errors.New("desktop keyring lookup failed")
}

func linuxKWalletPassword(ctx context.Context, service, account string) (string, error) {
	wallet := "kdewallet"
	if serviceName, walletPath := linuxKWalletServiceNameAndPath(); serviceName != "" {
		if output, err := exec.CommandContext(ctx, "dbus-send", "--session", "--print-reply=literal", "--dest="+serviceName, walletPath, "org.kde.KWallet.networkWallet").Output(); err == nil {
			if name := strings.Trim(strings.ReplaceAll(string(output), "\"", ""), " \r\n\t"); name != "" {
				wallet = name
			}
		}
	}
	output, err := exec.CommandContext(ctx, "kwallet-query", "--read-password", service, "--folder", account+" Keys", wallet).Output()
	if err != nil {
		return "", err
	}
	password := strings.TrimSpace(string(output))
	if password == "" || strings.HasPrefix(strings.ToLower(password), "failed to read") {
		return "", errors.New("KWallet lookup failed")
	}
	return password, nil
}

func linuxKWalletServiceNameAndPath() (string, string) {
	switch strings.TrimSpace(os.Getenv("KDE_SESSION_VERSION")) {
	case "6":
		return "org.kde.kwalletd6", "/modules/kwalletd6"
	case "5":
		return "org.kde.kwalletd5", "/modules/kwalletd5"
	default:
		return "org.kde.kwalletd", "/modules/kwalletd"
	}
}

func decryptChromiumCookie(encrypted []byte, metaVersion int64, safeStoragePassword string) (string, bool) {
	if len(encrypted) < 3 || (string(encrypted[:3]) != "v10" && string(encrypted[:3]) != "v11") {
		return "", false
	}
	password := "peanuts"
	if string(encrypted[:3]) == "v11" {
		password = safeStoragePassword
		if password == "" {
			return "", false
		}
	}
	key := pbkdf2.Key([]byte(password), []byte("saltysalt"), 1, 16, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil || (len(encrypted)-3)%aes.BlockSize != 0 {
		return "", false
	}
	plain := make([]byte, len(encrypted)-3)
	cipher.NewCBCDecrypter(block, []byte("                ")).CryptBlocks(plain, encrypted[3:])
	if len(plain) == 0 {
		return "", false
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(plain) {
		return "", false
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return "", false
		}
	}
	plain = plain[:len(plain)-padding]
	if metaVersion >= 24 && len(plain) >= 32 {
		plain = plain[32:]
	}
	value := strings.TrimLeft(string(plain), "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\v\f\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f")
	return value, value != ""
}

func chromiumExpiry(value int64) time.Time {
	const unixEpochDiffMicros = int64(11644473600000000)
	return time.Unix(0, (value-unixEpochDiffMicros)*1000).UTC()
}

func cookieDomainMatches(cookieDomain, host string) bool {
	domain := strings.TrimPrefix(strings.ToLower(cookieDomain), ".")
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func cookiePathMatches(requestPath, cookiePath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	return requestPath == cookiePath || strings.HasPrefix(requestPath, strings.TrimSuffix(cookiePath, "/")+"/") || cookiePath == "/"
}

func chromiumSameSite(value int64) http.SameSite {
	switch value {
	case 2:
		return http.SameSiteStrictMode
	case 1:
		return http.SameSiteLaxMode
	case 0:
		return http.SameSiteNoneMode
	default:
		return http.SameSiteDefaultMode
	}
}

// Discover returns Euria cookie sessions without making network requests.
func Discover(ctx context.Context, source CookieSource, selector string) ([]Session, error) {
	if source == nil {
		return nil, errors.New("Chrome/Chromium cookie source is unavailable")
	}
	target, _ := url.Parse(euriaOrigin)
	profiles, err := source.Profiles(ctx, selector)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("%w; sign in to Euria or select a Chrome/Chromium profile with --chrome-profile", ErrNoSession)
	}

	var sessions []Session
	var cookieReadError error
	sessionPaths := make(map[string]struct{})
	for _, profile := range profiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cookies, err := source.Cookies(ctx, profile, target)
		if err != nil {
			if !errors.Is(err, ErrNoCookieDatabase) && cookieReadError == nil {
				cookieReadError = err
			}
			continue
		}
		filtered := euriaCookies(cookies, target)
		if len(filtered) == 0 {
			continue
		}
		key := profile.Browser + "\x00" + profile.Path
		if _, exists := sessionPaths[key]; exists {
			continue
		}
		sessionPaths[key] = struct{}{}
		session := Session{Profile: profile, Cookies: filtered}
		for _, cookie := range filtered {
			if cookie.Name == "EURIA-API-XSRF-TOKEN" {
				session.XsrfToken = cookie.Value
				break
			}
		}
		sessions = append(sessions, session)
	}
	if len(sessions) == 0 && cookieReadError != nil {
		return nil, errors.New("could not read Chrome/Chromium cookies; unlock the desktop keyring, close the browser, or select a readable profile")
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("%w; sign in to Euria in Chrome/Chromium and retry", ErrNoSession)
	}
	return sessions, nil
}

func (s *ChromiumCookieSource) discoverProfiles() []Profile {
	var profiles []Profile
	seen := make(map[string]struct{})
	for _, root := range s.profileRoots() {
		entries, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || (entry.Name() != "Default" && !strings.HasPrefix(entry.Name(), "Profile ")) {
				continue
			}
			path := filepath.Join(root.path, entry.Name())
			key := root.browser + "\x00" + path
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			profiles = append(profiles, Profile{Browser: root.browser, Name: entry.Name(), Path: path})
		}
	}
	return profiles
}

func (s *ChromiumCookieSource) profileRoots() []profileRoot {
	configHome := s.configHome
	if configHome == "" && s.homeDir != "" {
		configHome = filepath.Join(s.homeDir, ".config")
	}
	var roots []profileRoot
	if configHome != "" {
		roots = append(roots,
			profileRoot{browser: "chrome", path: filepath.Join(configHome, "google-chrome")},
			profileRoot{browser: "chromium", path: filepath.Join(configHome, "chromium")},
		)
	}
	if s.homeDir != "" {
		roots = append(roots,
			profileRoot{browser: "chrome", path: filepath.Join(s.homeDir, ".var/app/com.google.Chrome/config/google-chrome")},
			profileRoot{browser: "chromium", path: filepath.Join(s.homeDir, ".var/app/org.chromium.Chromium/config/chromium")},
			profileRoot{browser: "chromium", path: filepath.Join(s.homeDir, "snap/chromium/common/chromium")},
		)
	}
	return roots
}

func (s *ChromiumCookieSource) profilesAtPath(selector string) ([]Profile, error) {
	path, err := filepath.Abs(selector)
	if err != nil {
		return nil, fmt.Errorf("resolve Chrome/Chromium profile path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("Chrome/Chromium profile path %q is not readable", selector)
	}
	if info.IsDir() && !hasCookieDatabase(path) {
		profiles := s.profilesUnderRoot(path)
		if len(profiles) != 0 {
			return profiles, nil
		}
		if isKnownProfileRoot(path, s.profileRoots()) {
			return nil, fmt.Errorf("Chrome/Chromium profile path %q has no Cookies database", selector)
		}
		browsers := browsersForPath(path, s.profileRoots())
		name := filepath.Base(path)
		profiles = make([]Profile, 0, len(browsers))
		for _, browser := range browsers {
			profiles = append(profiles, Profile{Browser: browser, Name: name, Path: path})
		}
		return profiles, nil
	}
	if info.IsDir() {
		browsers := browsersForPath(path, s.profileRoots())
		name := filepath.Base(path)
		profiles := make([]Profile, 0, len(browsers))
		for _, browser := range browsers {
			profiles = append(profiles, Profile{Browser: browser, Name: name, Path: path})
		}
		return profiles, nil
	}
	if filepath.Base(path) != "Cookies" {
		return nil, fmt.Errorf("Chrome/Chromium profile path %q must be a profile directory or Cookies database", selector)
	}
	browsers := browsersForPath(path, s.profileRoots())
	profileDir := filepath.Dir(path)
	if filepath.Base(profileDir) == "Network" {
		profileDir = filepath.Dir(profileDir)
	}
	profiles := make([]Profile, 0, len(browsers))
	for _, browser := range browsers {
		profiles = append(profiles, Profile{Browser: browser, Name: filepath.Base(profileDir), Path: path})
	}
	return profiles, nil
}

func (s *ChromiumCookieSource) profilesUnderRoot(path string) []Profile {
	var profiles []Profile
	for _, root := range s.profileRoots() {
		if !pathWithin(path, root.path) && !pathWithin(root.path, path) {
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || (entry.Name() != "Default" && !strings.HasPrefix(entry.Name(), "Profile ")) {
				continue
			}
			profilePath := filepath.Join(path, entry.Name())
			profiles = append(profiles, Profile{Browser: root.browser, Name: entry.Name(), Path: profilePath})
		}
	}
	return profiles
}

func isKnownProfileRoot(path string, roots []profileRoot) bool {
	for _, root := range roots {
		if filepath.Clean(path) == filepath.Clean(root.path) {
			return true
		}
	}
	return false
}

func hasCookieDatabase(profilePath string) bool {
	for _, path := range []string{filepath.Join(profilePath, "Network", "Cookies"), filepath.Join(profilePath, "Cookies")} {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func isPathSelector(selector string) bool {
	return filepath.IsAbs(selector) || strings.ContainsRune(selector, filepath.Separator)
}

func browsersForPath(path string, roots []profileRoot) []string {
	for _, root := range roots {
		if pathWithin(path, root.path) {
			return []string{root.browser}
		}
	}
	profilePath := filepath.Clean(path)
	if filepath.Base(profilePath) == "Cookies" {
		profilePath = filepath.Dir(profilePath)
		if filepath.Base(profilePath) == "Network" {
			profilePath = filepath.Dir(profilePath)
		}
	}
	profileName := strings.ToLower(filepath.Base(profilePath))
	if strings.Contains(profileName, "chromium") {
		return []string{"chromium"}
	}
	if strings.Contains(profileName, "chrome") {
		return []string{"chrome"}
	}
	return []string{"chrome", "chromium"}
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func euriaCookies(cookies []*http.Cookie, target *url.URL) []*http.Cookie {
	filtered := make([]*http.Cookie, 0, len(cookies))
	host := strings.ToLower(target.Hostname())
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		cookiePath := cookie.Path
		if cookiePath == "" {
			cookiePath = "/"
		}
		if !strings.HasPrefix(cookiePath, "/") || (cookiePath != "/" && target.Path != cookiePath) {
			continue
		}
		copy := *cookie
		filtered = append(filtered, &copy)
	}
	return filtered
}
