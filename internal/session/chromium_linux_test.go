package session

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCookieSource struct {
	profiles        []Profile
	cookies         map[string][]*http.Cookie
	cookieErr       map[string]error
	profileSelector string
}

func (s *fakeCookieSource) Profiles(_ context.Context, selector string) ([]Profile, error) {
	s.profileSelector = selector
	if selector == "" {
		return append([]Profile(nil), s.profiles...), nil
	}
	var selected []Profile
	for _, profile := range s.profiles {
		if profile.Name == selector || filepath.Clean(profile.Path) == filepath.Clean(selector) {
			selected = append(selected, profile)
		}
	}
	return selected, nil
}

func (s *fakeCookieSource) Cookies(_ context.Context, profile Profile, _ *url.URL) ([]*http.Cookie, error) {
	if err := s.cookieErr[profile.Path]; err != nil {
		return nil, err
	}
	return s.cookies[profile.Path], nil
}

func TestDiscoverFindsChromeAndChromiumProfiles(t *testing.T) {
	source := &fakeCookieSource{
		profiles: []Profile{
			{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"},
			{Browser: "chromium", Name: "Profile 1", Path: "/profiles/chromium/Profile 1"},
		},
		cookies: map[string][]*http.Cookie{
			"/profiles/chrome/Default":     {{Name: "session", Value: "chrome-session", Domain: "euria.infomaniak.com", Path: "/"}},
			"/profiles/chromium/Profile 1": {{Name: "session", Value: "chromium-session", Domain: "euria.infomaniak.com", Path: "/"}},
		},
	}

	got, err := Discover(context.Background(), source, "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Discover() returned %d sessions, want 2", len(got))
	}
	if got[0].Profile.Browser != "chrome" || got[1].Profile.Browser != "chromium" {
		t.Fatalf("Discover() browsers = %q, %q; want chrome, chromium", got[0].Profile.Browser, got[1].Profile.Browser)
	}
}

func TestDiscoverFiltersCookiesToEuriaOrigin(t *testing.T) {
	profile := Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &fakeCookieSource{
		profiles: []Profile{profile},
		cookies: map[string][]*http.Cookie{
			profile.Path: {
				{Name: "session", Value: "valid", Domain: ".infomaniak.com", Path: "/", Secure: true},
				{Name: "other", Value: "foreign", Domain: "example.com", Path: "/", Secure: true},
				{Name: "suffix", Value: "foreign", Domain: "notinfomaniak.com", Path: "/", Secure: true},
			},
		},
	}

	got, err := Discover(context.Background(), source, "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || len(got[0].Cookies) != 1 || got[0].Cookies[0].Value != "valid" {
		t.Fatalf("Discover() cookies = %#v, want only the Infomaniak parent-domain cookie", got)
	}
}

func TestDiscoverExtractsXsrfToken(t *testing.T) {
	profile := Profile{Browser: "chromium", Name: "Default", Path: "/profiles/chromium/Default"}
	source := &fakeCookieSource{
		profiles: []Profile{profile},
		cookies: map[string][]*http.Cookie{
			profile.Path: {
				{Name: "EURIA-API-XSRF-TOKEN", Value: "xsrf-fixture", Domain: "euria.infomaniak.com", Path: "/", Secure: true},
			},
		},
	}

	got, err := Discover(context.Background(), source, "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].XsrfToken != "xsrf-fixture" {
		t.Fatalf("Discover() XSRF token = %#v, want fixture token", got)
	}
}

func TestDiscoverHonorsProfileSelector(t *testing.T) {
	profiles := []Profile{
		{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"},
		{Browser: "chrome", Name: "Profile 2", Path: "/profiles/chrome/Profile 2"},
	}
	source := &fakeCookieSource{
		profiles: profiles,
		cookies: map[string][]*http.Cookie{
			profiles[0].Path: {{Name: "session", Value: "one", Domain: "euria.infomaniak.com", Path: "/"}},
			profiles[1].Path: {{Name: "session", Value: "two", Domain: "euria.infomaniak.com", Path: "/"}},
		},
	}

	got, err := Discover(context.Background(), source, "Profile 2")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if source.profileSelector != "Profile 2" {
		t.Fatalf("Profiles() selector = %q, want %q", source.profileSelector, "Profile 2")
	}
	if len(got) != 1 || got[0].Profile.Name != "Profile 2" {
		t.Fatalf("Discover() profiles = %#v, want only Profile 2", got)
	}
}

func TestDiscoverReportsCookieAccessFailure(t *testing.T) {
	profile := Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &fakeCookieSource{
		profiles:  []Profile{profile},
		cookieErr: map[string]error{profile.Path: errors.New("keyring is locked")},
	}
	if _, err := Discover(context.Background(), source, ""); err == nil {
		t.Fatal("Discover() succeeded with an unreadable browser profile")
	}
}

func TestDiscoverKeepsReadableSessionWhenAnotherProfileIsUnreadable(t *testing.T) {
	valid := Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	locked := Profile{Browser: "chromium", Name: "Profile 2", Path: "/profiles/chromium/Profile 2"}
	source := &fakeCookieSource{
		profiles:  []Profile{locked, valid},
		cookies:   map[string][]*http.Cookie{valid.Path: {{Name: "EURIA_SESSION", Value: "session", Domain: "euria.infomaniak.com", Path: "/"}}},
		cookieErr: map[string]error{locked.Path: errors.New("keyring is locked")},
	}

	got, err := Discover(context.Background(), source, "")
	if err != nil {
		t.Fatalf("Discover() error = %v; want the readable signed-in profile", err)
	}
	if len(got) != 1 || got[0].Profile.Path != valid.Path {
		t.Fatalf("Discover() = %#v, want only readable profile %q", got, valid.Path)
	}
}

func TestChromiumCookieSourceDiscoversLinuxProfiles(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "xdg")
	profiles := []struct {
		browser string
		path    string
	}{
		{"chrome", filepath.Join(configHome, "google-chrome", "Default")},
		{"chrome", filepath.Join(configHome, "google-chrome", "Profile 2")},
		{"chromium", filepath.Join(configHome, "chromium", "Default")},
		{"chrome", filepath.Join(home, ".var/app/com.google.Chrome/config/google-chrome/Default")},
		{"chromium", filepath.Join(home, ".var/app/org.chromium.Chromium/config/chromium/Default")},
		{"chromium", filepath.Join(home, "snap/chromium/common/chromium/Default")},
	}
	for _, profile := range profiles {
		cookieDB := filepath.Join(profile.path, "Cookies")
		if err := os.MkdirAll(filepath.Dir(cookieDB), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cookieDB, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	source := ChromiumCookieSource{homeDir: home, configHome: configHome}
	got, err := source.Profiles(context.Background(), "")
	if err != nil {
		t.Fatalf("Profiles() error = %v", err)
	}
	if len(got) != len(profiles) {
		t.Fatalf("Profiles() returned %d profiles, want %d: %#v", len(got), len(profiles), got)
	}
	for _, profile := range profiles {
		found := false
		for _, candidate := range got {
			if candidate.Browser == profile.browser && candidate.Path == profile.path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Profiles() omitted %s profile %q", profile.browser, profile.path)
		}
	}
}

func TestChromiumCookieSourceDiscoversProfileWithoutCookieDatabase(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "google-chrome", "Default")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	source := ChromiumCookieSource{configHome: filepath.Dir(filepath.Dir(profilePath))}

	got, err := source.Profiles(context.Background(), "")
	if err != nil {
		t.Fatalf("Profiles() error = %v", err)
	}
	if len(got) != 1 || got[0].Browser != "chrome" || got[0].Path != profilePath {
		t.Fatalf("Profiles() = %#v, want the clean Chrome profile %q", got, profilePath)
	}
	_, err = (&source).Cookies(context.Background(), got[0], mustURL(t, euriaOrigin))
	if !errors.Is(err, ErrNoCookieDatabase) {
		t.Fatalf("Cookies() error = %v, want ErrNoCookieDatabase", err)
	}
}

func TestChromiumCookieSourceHonorsExplicitProfilePath(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "my-chromium-profile")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "Cookies"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	source := ChromiumCookieSource{}
	got, err := source.Profiles(context.Background(), profilePath)
	if err != nil {
		t.Fatalf("Profiles() error = %v", err)
	}
	if len(got) != 1 || got[0].Path != profilePath || got[0].Browser != "chromium" {
		t.Fatalf("Profiles() = %#v, want the Chromium reader for the explicit path", got)
	}
}

func TestChromiumCookieSourceFlagsBrowserAmbiguityForCustomPath(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "Default")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "Cookies"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	source := ChromiumCookieSource{}
	got, err := source.Profiles(context.Background(), profilePath)
	if err != nil {
		t.Fatalf("Profiles() error = %v", err)
	}
	if len(got) != 2 || got[0].Path != profilePath || got[1].Path != profilePath || got[0].Browser == got[1].Browser {
		t.Fatalf("Profiles() = %#v, want both browser interpretations of one custom path", got)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestChromiumCookieSourceRejectsForeignPort(t *testing.T) {
	source := ChromiumCookieSource{}
	target, err := url.Parse("https://euria.infomaniak.com:8443/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Cookies(context.Background(), Profile{Browser: "chrome", Path: "/unused"}, target)
	if err == nil || !strings.Contains(err.Error(), "Euria HTTPS origin") {
		t.Fatalf("Cookies() error = %v, want the exact Euria origin restriction", err)
	}
}
