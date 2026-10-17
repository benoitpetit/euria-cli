package session

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steipete/sweetcookie"
	_ "modernc.org/sqlite"
)

func TestSweetcookieSourceUsesFixedEuriaURL(t *testing.T) {
	var calls int
	source := &SweetcookieCookieSource{getCookies: func(_ context.Context, options sweetcookie.Options) (sweetcookie.Result, error) {
		calls++
		if options.URL != "https://euria.infomaniak.com/" {
			t.Errorf("Sweetcookie URL = %q, want fixed Euria origin", options.URL)
		}
		if options.Mode != sweetcookie.ModeMerge {
			t.Errorf("Sweetcookie mode = %q, want merge", options.Mode)
		}
		if len(options.Browsers) != 1 {
			t.Errorf("Sweetcookie browsers = %v, want one browser per call", options.Browsers)
		}
		if len(options.Browsers) == 1 {
			browser := Browser(options.Browsers[0])
			if want := defaultProfile(browser); want != "" && options.Profiles[options.Browsers[0]] != want {
				t.Errorf("profile for %s = %q, want default %q", browser, options.Profiles[options.Browsers[0]], want)
			}
		}
		return sweetcookie.Result{}, nil
	}}

	if _, err := source.Discover(context.Background(), DiscoveryOptions{}); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := len(sweetcookie.DefaultBrowsers()); calls != want {
		t.Errorf("Sweetcookie calls = %d, want one call for each of %d default browsers", calls, want)
	}
}

func TestSweetcookieSourceKeepsDuplicateCookiesFromDifferentBrowsers(t *testing.T) {
	firefoxDB := writeFirefoxCookieDB(t, "firefox", "firefox-session", "")
	zenDB := writeFirefoxCookieDB(t, "zen", "zen-session", "")
	source := NewSweetcookieCookieSource()
	result, err := source.Discover(context.Background(), DiscoveryOptions{
		Browsers: []Browser{"firefox", "zen"},
		Profiles: map[Browser]string{"firefox": firefoxDB, "zen": zenDB},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Sessions) != 2 {
		t.Fatalf("Discover() sessions = %d, want both browser sessions; warnings=%v", len(result.Sessions), result.Warnings)
	}
	got := map[Browser]string{}
	for _, userSession := range result.Sessions {
		got[userSession.Profile.Browser] = userSession.Cookies[0].Value
	}
	if got["firefox"] != "firefox-session" || got["zen"] != "zen-session" {
		t.Fatalf("sessions = %v, want Firefox and Zen cookies preserved separately", got)
	}
}

func TestSweetcookieSourceSelectsFirefoxDefaultProfile(t *testing.T) {
	home := t.TempDir()
	var root string
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", home)
		root = filepath.Join(home, "Library", "Application Support", "Firefox")
	case "linux":
		t.Setenv("HOME", home)
		root = filepath.Join(home, ".mozilla", "firefox")
	case "windows":
		appData := filepath.Join(home, "AppData", "Roaming")
		t.Setenv("APPDATA", appData)
		root = filepath.Join(appData, "Mozilla", "Firefox")
	default:
		t.Skip("unsupported OS for Firefox profile discovery")
	}
	defaultDir := filepath.Join(root, "Profiles", "default-release")
	workDir := filepath.Join(root, "Profiles", "work")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(
		"[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/default-release\nDefault=1\n\n"+
			"[Profile1]\nName=work\nIsRelative=1\nPath=Profiles/work\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFirefoxCookieDBAt(t, defaultDir, "default-session", "")
	writeFirefoxCookieDBAt(t, workDir, "work-session", "")

	result, err := NewSweetcookieCookieSource().Discover(context.Background(), DiscoveryOptions{Browsers: []Browser{"firefox"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Profile.Name != "default-release" || result.Sessions[0].Cookies[0].Value != "default-session" {
		t.Fatalf("Discover() sessions = %#v, want only default-release profile", result.Sessions)
	}
}

func TestSweetcookieSourceExcludesFirefoxContainerCookies(t *testing.T) {
	result := groupCookies(sweetcookie.Result{Cookies: []sweetcookie.Cookie{
		{Name: "EURIA_SESSION", Value: "default-session", Domain: "euria.infomaniak.com", Path: "/"},
		{
			Name: "EURIA_SESSION", Value: "container-session", Domain: "euria.infomaniak.com", Path: "/",
			Container: sweetcookie.Container{ID: 2, Name: "Work"},
			Source:    sweetcookie.Source{Browser: sweetcookie.BrowserFirefox, Profile: "default-release", StorePath: "/firefox/cookies.sqlite"},
		},
	}}, time.Now())
	if len(result.Sessions) != 1 || len(result.Sessions[0].Cookies) != 1 || result.Sessions[0].Cookies[0].Value != "default-session" {
		t.Fatalf("container cookies were not excluded: %#v", result.Sessions)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "container") || strings.Contains(result.Warnings[0], "container-session") {
		t.Fatalf("container warning = %v, want safe actionable diagnostic", result.Warnings)
	}
}

func writeFirefoxCookieDB(t *testing.T, profile, value, originAttributes string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), profile)
	return writeFirefoxCookieDBAt(t, dir, value, originAttributes)
}

func writeFirefoxCookieDBAt(t *testing.T, dir, value, originAttributes string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "cookies.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE moz_cookies(host TEXT, name TEXT, value TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER, sameSite INTEGER, originAttributes TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO moz_cookies(host,name,value,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes) VALUES(?,?,?,?,?,?,?,?,?)`,
		"euria.infomaniak.com", "EURIA_SESSION", value, "/", time.Now().Add(time.Hour).Unix(), 1, 1, 2, originAttributes,
	); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestSweetcookieSourceGroupsCookiesByBrowserAndProfile(t *testing.T) {
	profileCookie := func(name, value, profile, storePath string, browser sweetcookie.Browser) sweetcookie.Cookie {
		return sweetcookie.Cookie{
			Name: name, Value: value, Domain: "euria.infomaniak.com", Path: "/",
			Source: sweetcookie.Source{Browser: browser, Profile: profile, StorePath: storePath},
		}
	}
	allCookies := []sweetcookie.Cookie{
		profileCookie("session", "chrome-a", "Default", "/chrome/Default/Cookies", sweetcookie.BrowserChrome),
		profileCookie("csrf", "csrf-a", "Default", "/chrome/Default/Cookies", sweetcookie.BrowserChrome),
		profileCookie("session", "chrome-b", "Profile 2", "/chrome/Profile 2/Cookies", sweetcookie.BrowserChrome),
		profileCookie("session", "edge-a", "Default", "/edge/Default/Cookies", sweetcookie.BrowserEdge),
		profileCookie("session", "chrome-other-store", "Default", "/chrome/Default/OtherCookies", sweetcookie.BrowserChrome),
	}
	source := &SweetcookieCookieSource{getCookies: func(_ context.Context, options sweetcookie.Options) (sweetcookie.Result, error) {
		var cookies []sweetcookie.Cookie
		for _, cookie := range allCookies {
			if cookie.Source.Browser == options.Browsers[0] {
				cookies = append(cookies, cookie)
			}
		}
		return sweetcookie.Result{Cookies: cookies}, nil
	}}

	result, err := source.Discover(context.Background(), DiscoveryOptions{Browsers: []Browser{"chrome", "edge"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Sessions) != 4 {
		t.Fatalf("Discover() returned %d sessions, want one per browser/profile/store", len(result.Sessions))
	}
	want := map[string][]string{
		"chrome|Default|/chrome/Default/Cookies":      {"session", "csrf"},
		"chrome|Profile 2|/chrome/Profile 2/Cookies":  {"session"},
		"edge|Default|/edge/Default/Cookies":          {"session"},
		"chrome|Default|/chrome/Default/OtherCookies": {"session"},
	}
	for _, got := range result.Sessions {
		key := string(got.Profile.Browser) + "|" + got.Profile.Name + "|" + got.Profile.Path
		wantNames, ok := want[key]
		if !ok {
			t.Errorf("unexpected session source %q", key)
			continue
		}
		gotNames := make([]string, 0, len(got.Cookies))
		for _, cookie := range got.Cookies {
			gotNames = append(gotNames, cookie.Name)
		}
		if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
			t.Errorf("session %q cookie names = %v, want %v", key, gotNames, wantNames)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing session sources: %v", want)
	}
}

func TestSweetcookieSourceFiltersForeignExpiredAndPathScopedCookies(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	source := &SweetcookieCookieSource{getCookies: func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{
			{Name: "parent", Value: "keep-parent", Domain: ".infomaniak.com", Path: "/"},
			{Name: "euria", Value: "keep-euria", Domain: "euria.infomaniak.com", Path: "/"},
			{Name: "foreign", Value: "drop-foreign", Domain: "example.com", Path: "/"},
			{Name: "suffix", Value: "drop-suffix", Domain: "notinfomaniak.com", Path: "/"},
			{Name: "expired", Value: "drop-expired", Domain: "euria.infomaniak.com", Path: "/", Expires: &expired},
			{Name: "scoped", Value: "drop-scoped", Domain: "euria.infomaniak.com", Path: "/api/"},
		}}, nil
	}}

	result, err := source.Discover(context.Background(), DiscoveryOptions{Browsers: []Browser{"chrome"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("Discover() sessions = %d, want 1", len(result.Sessions))
	}
	gotValues := make([]string, 0, len(result.Sessions[0].Cookies))
	for _, cookie := range result.Sessions[0].Cookies {
		gotValues = append(gotValues, cookie.Value)
	}
	if strings.Join(gotValues, ",") != "keep-parent,keep-euria" {
		t.Fatalf("eligible cookie values = %v, want parent-domain and Euria cookies only", gotValues)
	}
}

func TestSweetcookieSourcePreservesXSRFAndCookieAttributes(t *testing.T) {
	expires := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
	source := &SweetcookieCookieSource{getCookies: func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{
			Name: "EURIA-API-XSRF-TOKEN", Value: "xsrf-fixture", Domain: "euria.infomaniak.com", Path: "/",
			Secure: true, HTTPOnly: true, SameSite: sweetcookie.SameSiteStrict, Expires: &expires,
			Source: sweetcookie.Source{Browser: sweetcookie.BrowserFirefox, Profile: "default", StorePath: "/firefox/cookies.sqlite"},
		}}}, nil
	}}

	result, err := source.Discover(context.Background(), DiscoveryOptions{Browsers: []Browser{"firefox"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("Discover() sessions = %d, want 1", len(result.Sessions))
	}
	got := result.Sessions[0]
	if got.XsrfToken != "xsrf-fixture" {
		t.Errorf("XsrfToken = %q, want token from Euria cookie", got.XsrfToken)
	}
	cookie := got.Cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || !cookie.Expires.Equal(expires) {
		t.Errorf("cookie attributes not preserved: %#v", cookie)
	}
}

func TestSweetcookieSourceWarningsDoNotExposeCookieValues(t *testing.T) {
	secret := "cookie-secret-never-print"
	source := &SweetcookieCookieSource{getCookies: func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{
			Cookies:  []sweetcookie.Cookie{{Name: "session", Value: secret, Domain: "euria.infomaniak.com", Path: "/"}},
			Warnings: []string{"browser warning contains " + secret},
		}, nil
	}}

	result, err := source.Discover(context.Background(), DiscoveryOptions{Browsers: []Browser{"chrome"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("Discover() warnings = %v, want one sanitized warning", result.Warnings)
	}
	if strings.Contains(result.Warnings[0], secret) {
		t.Fatalf("warning exposed a cookie value: %q", result.Warnings[0])
	}
}
