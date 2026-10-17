package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/pbkdf2"
	_ "modernc.org/sqlite"
)

func TestChromiumCookieSourceReadsProfileWithoutTemporaryDatabase(t *testing.T) {
	profile := testCookieProfile(t, "chrome")
	writeCookieDatabase(t, filepath.Join(profile.Path, "Network", "Cookies"), []cookieFixture{{
		host: ".euria.infomaniak.com", name: "EURIA_SESSION", path: "/", value: "session-fixture",
	}})
	missingTemp := filepath.Join(t.TempDir(), "does-not-exist")
	t.Setenv("TMPDIR", missingTemp)
	target, _ := url.Parse(euriaOrigin)

	cookies, err := NewChromiumCookieSource().Cookies(context.Background(), profile, target)
	if err != nil {
		t.Fatalf("Cookies() error = %v, want direct read without a temp snapshot", err)
	}
	if len(cookies) != 1 || cookies[0].Value != "session-fixture" {
		t.Fatalf("Cookies() = %#v, want the Euria session fixture", cookies)
	}
}

func TestChromiumCookieSourceUsesChromiumApplicationKeyringAttribute(t *testing.T) {
	for _, browser := range []string{"chrome", "chromium"} {
		t.Run(browser, func(t *testing.T) {
			profile := testCookieProfile(t, browser)
			writeCookieDatabase(t, filepath.Join(profile.Path, "Cookies"), []cookieFixture{{
				host: ".euria.infomaniak.com", name: "EURIA_SESSION", path: "/", encrypted: encryptChromiumCookie("session-secret-fixture", "fixture-safe-storage-password", "v11"),
			}})
			binDir := t.TempDir()
			argsPath := filepath.Join(t.TempDir(), "secret-tool-args")
			secretTool := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$SECRET_TOOL_ARGS\"\n" +
				"if [ \"$1\" = lookup ] && [ \"$2\" = application ] && { [ \"$3\" = chrome ] || [ \"$3\" = chromium ]; }; then printf '%s\\n' fixture-safe-storage-password; exit 0; fi\nexit 1\n"
			if err := os.WriteFile(filepath.Join(binDir, "secret-tool"), []byte(secretTool), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SECRET_TOOL_ARGS", argsPath)
			target, _ := url.Parse(euriaOrigin)

			cookies, err := NewChromiumCookieSource().Cookies(context.Background(), profile, target)
			if err != nil {
				t.Fatalf("Cookies() error = %v, want Chromium's application-key lookup", err)
			}
			args, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read secret-tool arguments: %v", err)
			}
			if want := "lookup application " + browser; strings.TrimSpace(string(args)) != want {
				t.Fatalf("secret-tool arguments = %q, want %q", args, want)
			}
			if len(cookies) != 1 || cookies[0].Value != "session-secret-fixture" {
				t.Fatalf("Cookies() = %#v, want decrypted Euria session", cookies)
			}
		})
	}
}

func TestChromiumCookieSourceFallsBackToKWallet(t *testing.T) {
	profile := testCookieProfile(t, "chrome")
	writeCookieDatabase(t, filepath.Join(profile.Path, "Cookies"), []cookieFixture{{
		host: ".euria.infomaniak.com", name: "EURIA_SESSION", path: "/", encrypted: encryptChromiumCookie("session-secret-fixture", "fixture-safe-storage-password", "v11"),
	}})
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "kwallet-query-args")
	for name, script := range map[string]string{
		"dbus-send":     "#!/bin/sh\nexit 1\n",
		"secret-tool":   "#!/bin/sh\nexit 1\n",
		"kwallet-query": "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$KWALLET_QUERY_ARGS\"\nprintf '%s\\n' fixture-safe-storage-password\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KWALLET_QUERY_ARGS", argsPath)
	target, _ := url.Parse(euriaOrigin)

	cookies, err := NewChromiumCookieSource().Cookies(context.Background(), profile, target)
	if err != nil {
		t.Fatalf("Cookies() error = %v, want KWallet fallback", err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read KWallet arguments: %v", err)
	}
	want := "--read-password Chrome Safe Storage --folder Chrome Keys kdewallet"
	if strings.TrimSpace(string(args)) != want {
		t.Fatalf("kwallet-query arguments = %q, want %q", args, want)
	}
	if len(cookies) != 1 || cookies[0].Value != "session-secret-fixture" {
		t.Fatalf("Cookies() = %#v, want decrypted Euria session", cookies)
	}
}

func TestChromiumCookieSourceReportsCookieDecryptionFailure(t *testing.T) {
	profile := testCookieProfile(t, "chrome")
	writeCookieDatabase(t, filepath.Join(profile.Path, "Cookies"), []cookieFixture{{
		host: ".euria.infomaniak.com", name: "EURIA_SESSION", path: "/", encrypted: []byte("v99bad-encrypted-value"),
	}})
	target, _ := url.Parse(euriaOrigin)

	_, err := NewChromiumCookieSource().Cookies(context.Background(), profile, target)
	if err == nil || !strings.Contains(err.Error(), "decrypt") || strings.Contains(err.Error(), "v99bad") {
		t.Fatalf("Cookies() error = %v, want a generic decryption failure", err)
	}
}

func TestDecryptChromiumCookieVersions(t *testing.T) {
	for _, fixture := range []struct {
		prefix   string
		version  int64
		password string
	}{
		{prefix: "v10", version: 23, password: "peanuts"},
		{prefix: "v11", version: 23, password: "fixture-safe-storage-password"},
		{prefix: "v10", version: 24, password: "peanuts"},
		{prefix: "v11", version: 24, password: "fixture-safe-storage-password"},
	} {
		t.Run(fixture.prefix+"-meta-"+strconv.FormatInt(fixture.version, 10), func(t *testing.T) {
			plain := "session-fixture"
			if fixture.version >= 24 {
				plain = strings.Repeat("h", 32) + plain
			}
			encrypted := encryptChromiumCookie(plain, fixture.password, fixture.prefix)
			got, ok := decryptChromiumCookie(encrypted, fixture.version, fixture.password)
			if !ok || got != "session-fixture" {
				t.Fatalf("decryptChromiumCookie() = %q, %v, want session-fixture, true", got, ok)
			}
		})
	}
}

type cookieFixture struct {
	host      string
	name      string
	path      string
	value     string
	encrypted []byte
}

func testCookieProfile(t *testing.T, browser string) Profile {
	t.Helper()
	profilePath := filepath.Join(t.TempDir(), "Default")
	if err := os.MkdirAll(filepath.Join(profilePath, "Network"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Profile{Browser: browser, Name: "Default", Path: profilePath}
}

func writeCookieDatabase(t *testing.T, path string, fixtures []cookieFixture) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO meta (key, value) VALUES ('version', '23')`,
		`CREATE TABLE cookies (host_key TEXT, name TEXT, path TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, is_secure INTEGER, is_httponly INTEGER, samesite INTEGER)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create cookie fixture schema: %v", err)
		}
	}
	for _, fixture := range fixtures {
		if _, err := db.Exec(`INSERT INTO cookies (host_key, name, path, value, encrypted_value, expires_utc, is_secure, is_httponly, samesite) VALUES (?, ?, ?, ?, ?, 0, 1, 1, 1)`, fixture.host, fixture.name, fixture.path, fixture.value, fixture.encrypted); err != nil {
			t.Fatalf("insert cookie fixture: %v", err)
		}
	}
}

func encryptChromiumCookie(value, password, prefix string) []byte {
	key := pbkdf2.Key([]byte(password), []byte("saltysalt"), 1, 16, sha1.New)
	block, _ := aes.NewCipher(key)
	padding := aes.BlockSize - len(value)%aes.BlockSize
	plain := []byte(value + strings.Repeat(string(rune(padding)), padding))
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, []byte("                ")).CryptBlocks(encrypted, plain)
	return append([]byte(prefix), encrypted...)
}
