package session

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/steipete/sweetcookie"
)

func TestSweetcookieSourceUsesFixedEuriaURL(t *testing.T) {
	var gotOptions sweetcookie.Options
	source := &SweetcookieCookieSource{getCookies: func(_ context.Context, options sweetcookie.Options) (sweetcookie.Result, error) {
		gotOptions = options
		return sweetcookie.Result{}, nil
	}}

	if _, err := source.Discover(context.Background(), DiscoveryOptions{}); err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if gotOptions.URL != "https://euria.infomaniak.com/" {
		t.Errorf("Sweetcookie URL = %q, want fixed Euria origin", gotOptions.URL)
	}
	if gotOptions.Mode != sweetcookie.ModeMerge {
		t.Errorf("Sweetcookie mode = %q, want merge", gotOptions.Mode)
	}
	if len(gotOptions.Browsers) == 0 {
		t.Fatal("Sweetcookie browsers are empty; defaults should be selected explicitly")
	}
}

func TestSweetcookieSourceGroupsCookiesByBrowserAndProfile(t *testing.T) {
	profileCookie := func(name, value, profile, storePath string, browser sweetcookie.Browser) sweetcookie.Cookie {
		return sweetcookie.Cookie{
			Name: name, Value: value, Domain: "euria.infomaniak.com", Path: "/",
			Source: sweetcookie.Source{Browser: browser, Profile: profile, StorePath: storePath},
		}
	}
	source := &SweetcookieCookieSource{getCookies: func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{
			profileCookie("session", "chrome-a", "Default", "/chrome/Default/Cookies", sweetcookie.BrowserChrome),
			profileCookie("csrf", "csrf-a", "Default", "/chrome/Default/Cookies", sweetcookie.BrowserChrome),
			profileCookie("session", "chrome-b", "Profile 2", "/chrome/Profile 2/Cookies", sweetcookie.BrowserChrome),
			profileCookie("session", "edge-a", "Default", "/edge/Default/Cookies", sweetcookie.BrowserEdge),
			profileCookie("session", "chrome-other-store", "Default", "/chrome/Default/OtherCookies", sweetcookie.BrowserChrome),
		}}, nil
	}}

	result, err := source.Discover(context.Background(), DiscoveryOptions{})
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

	result, err := source.Discover(context.Background(), DiscoveryOptions{})
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

	result, err := source.Discover(context.Background(), DiscoveryOptions{})
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

	result, err := source.Discover(context.Background(), DiscoveryOptions{})
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
