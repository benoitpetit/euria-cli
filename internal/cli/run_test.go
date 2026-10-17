package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benoitpetit/euria_retro/internal/session"
)

type workflowCookieSource struct {
	profiles        []session.Profile
	cookies         map[string][]*http.Cookie
	err             error
	profileSelector string
	profileCalls    int
}

func (s *workflowCookieSource) Profiles(_ context.Context, selector string) ([]session.Profile, error) {
	s.profileCalls++
	s.profileSelector = selector
	if s.err != nil || selector == "" {
		return append([]session.Profile(nil), s.profiles...), s.err
	}
	var selected []session.Profile
	for _, profile := range s.profiles {
		if profile.Name == selector || filepath.Clean(profile.Path) == filepath.Clean(selector) {
			selected = append(selected, profile)
		}
	}
	return selected, nil
}

func (s *workflowCookieSource) Cookies(_ context.Context, profile session.Profile, _ *url.URL) ([]*http.Cookie, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cookies[profile.Path], nil
}

type workflowBrowserLauncher struct {
	profiles []session.Profile
	targets  []string
	err      error
	onLaunch func(session.Profile)
}

func (l *workflowBrowserLauncher) Launch(profile session.Profile, target string) error {
	l.profiles = append(l.profiles, profile)
	l.targets = append(l.targets, target)
	if l.onLaunch != nil {
		l.onLaunch(profile)
	}
	return l.err
}

func workflowSessionSource() *workflowCookieSource {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/test/chrome/Default"}
	return &workflowCookieSource{
		profiles: []session.Profile{profile},
		cookies: map[string][]*http.Cookie{
			profile.Path: {
				{Name: "EURIA_SESSION", Value: "session-cookie-secret", Path: "/"},
				{Name: "EURIA-API-XSRF-TOKEN", Value: "xsrf-token-secret", Path: "/"},
			},
		},
	}
}

func TestRunDefaultsToAuthenticatedMode(t *testing.T) {
	source := workflowSessionSource()
	guestCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/1/login":
			w.WriteHeader(http.StatusOK)
		case "/api/1/users/me/accounts":
			_, _ = io.WriteString(w, `[{"id":42}]`)
		case "/api/1/accounts/42/conversations":
			_, _ = io.WriteString(w, "data: {\"id\":\"conv-auth\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"authenticated answer\"}}]}\n\ndata: </stream>")
		case "/api/1/public/conversations/ephemeral":
			guestCalls++
			http.Error(w, "unexpected guest call", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello", "--output", "json"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin:  server.URL,
		CookieSource: source,
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if guestCalls != 0 || source.profileCalls != 1 {
		t.Fatalf("guest calls = %d, profile discovery calls = %d; want 0 and 1", guestCalls, source.profileCalls)
	}
	var result struct {
		Answer         string `json:"answer"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Run() JSON = %q: %v", stdout.String(), err)
	}
	if result.Answer != "authenticated answer" || result.ConversationID != "conv-auth" {
		t.Fatalf("Run() result = %#v", result)
	}
}

func TestRunWithoutArgumentsAndEmptyInputShowsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), nil, strings.NewReader(" \n\t "), &stdout, &stderr, Dependencies{
		CookieSource: &workflowCookieSource{err: errors.New("cookie source must not be touched")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want help to exit successfully", err)
	}
	if stdout.String() != Usage() {
		t.Fatalf("Run() output = %q, want complete help", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run() stderr = %q, want empty", stderr.String())
	}
}

func TestRunWithoutArgumentsAndPipedPromptStartsChat(t *testing.T) {
	source := workflowSessionSource()
	chatRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/1/login":
			w.WriteHeader(http.StatusOK)
		case "/api/1/users/me/accounts":
			_, _ = io.WriteString(w, `[{"id":42}]`)
		case "/api/1/accounts/42/conversations":
			chatRequests++
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode chat request: %v", err)
			}
			if payload.Content != "piped prompt" {
				t.Errorf("chat content = %q, want piped prompt", payload.Content)
			}
			_, _ = io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"piped answer\"}}]}\n\ndata: </stream>")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), nil, strings.NewReader("  piped prompt\n"), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "piped answer\n" || chatRequests != 1 {
		t.Fatalf("Run() output = %q, chat requests = %d; want piped answer from exactly one request", stdout.String(), chatRequests)
	}
}

func TestRunAuthLoginOpensCleanChromeProfileWithoutCookiesDatabase(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, ".config")
	profilePath := filepath.Join(configHome, "google-chrome", "Default")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	launcher := &workflowBrowserLauncher{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("clean profile should not make an Euria request")
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want timeout after opening the new profile", err)
	}
	if len(launcher.profiles) != 1 || launcher.profiles[0].Path != profilePath || launcher.profiles[0].Browser != "chrome" {
		t.Fatalf("launched profiles = %#v, want clean Chrome profile %q", launcher.profiles, profilePath)
	}
}

func TestRunAuthLoginReusesSignedInProfileWithoutLaunching(t *testing.T) {
	source := workflowSessionSource()
	launcher := &workflowBrowserLauncher{}
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("login request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "Euria session: signed in\n" || len(launcher.profiles) != 0 || apiCalls != 1 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, launches = %d, API calls = %d", stdout.String(), stderr.String(), len(launcher.profiles), apiCalls)
	}
}

func TestRunAuthLoginLaunchesProfileAndWaitsForSession(t *testing.T) {
	profile := session.Profile{Browser: "chromium", Name: "Default", Path: "/test/chromium/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	launcher := &workflowBrowserLauncher{onLaunch: func(profile session.Profile) {
		source.cookies[profile.Path] = []*http.Cookie{{Name: "EURIA_SESSION", Value: "new-session", Path: "/"}}
	}}
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("login request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		if cookie, _ := r.Cookie("EURIA_SESSION"); cookie == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "Euria session: signed in\n" || stderr.String() == "" {
		t.Fatalf("stdout = %q, stderr = %q, want success and one progress line", stdout.String(), stderr.String())
	}
	if len(launcher.profiles) != 1 || launcher.profiles[0].Path != profile.Path || len(launcher.targets) != 1 || launcher.targets[0] != "https://euria.infomaniak.com/api/1/login" {
		t.Fatalf("launcher profiles = %#v, targets = %#v", launcher.profiles, launcher.targets)
	}
	if apiCalls != 1 {
		t.Fatalf("auth status calls = %d, want one GET after browser sign-in", apiCalls)
	}
}

func TestRunAuthLoginRequiresProfileWhenSeveralAreAvailable(t *testing.T) {
	first := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	second := session.Profile{Browser: "chromium", Name: "Default", Path: "/profiles/chromium/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{first, second}, cookies: map[string][]*http.Cookie{}}
	launcher := &workflowBrowserLauncher{}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher,
	})
	if err == nil || !strings.Contains(err.Error(), first.Path) || !strings.Contains(err.Error(), second.Path) || !strings.Contains(err.Error(), "--chrome-profile") {
		t.Fatalf("Run() error = %v, want both profile paths and selection guidance", err)
	}
	if len(launcher.profiles) != 0 {
		t.Fatalf("launched %#v before resolving profile ambiguity", launcher.profiles)
	}
}

func TestRunAuthLoginRequiresPathWhenProfileNameIsAmbiguous(t *testing.T) {
	first := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	second := session.Profile{Browser: "chromium", Name: "Default", Path: "/profiles/chromium/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{first, second}, cookies: map[string][]*http.Cookie{}}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--chrome-profile", "Default"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: &workflowBrowserLauncher{},
	})
	if err == nil || !strings.Contains(err.Error(), first.Path) || !strings.Contains(err.Error(), second.Path) {
		t.Fatalf("Run() error = %v, want both paths for ambiguous profile name", err)
	}
}

func TestRunAuthLoginUsesExplicitPathToDisambiguateProfiles(t *testing.T) {
	first := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	second := session.Profile{Browser: "chromium", Name: "Default", Path: "/profiles/chromium/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{first, second}, cookies: map[string][]*http.Cookie{}}
	launcher := &workflowBrowserLauncher{onLaunch: func(profile session.Profile) {
		source.cookies[profile.Path] = []*http.Cookie{{Name: "EURIA_SESSION", Value: "new-session", Path: "/"}}
	}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("login request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		if cookie, _ := r.Cookie("EURIA_SESSION"); cookie == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--chrome-profile", second.Path}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(launcher.profiles) != 1 || launcher.profiles[0].Path != second.Path {
		t.Fatalf("launched profiles = %#v, want explicit path %q", launcher.profiles, second.Path)
	}
}

func TestRunAuthLoginRequiresBrowserForAmbiguousCustomPath(t *testing.T) {
	path := "/profiles/custom/Default"
	source := &workflowCookieSource{
		profiles: []session.Profile{
			{Browser: "chrome", Name: "Default", Path: path},
			{Browser: "chromium", Name: "Default", Path: path},
		},
		cookies: map[string][]*http.Cookie{},
	}
	launcher := &workflowBrowserLauncher{}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--chrome-profile", path}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher,
	})
	if err == nil || !strings.Contains(err.Error(), "--browser chrome") || !strings.Contains(err.Error(), "--browser chromium") {
		t.Fatalf("Run() error = %v, want both browser choices for the custom profile path", err)
	}
	if len(launcher.profiles) != 0 {
		t.Fatalf("launched %#v before resolving browser ambiguity", launcher.profiles)
	}
}

func TestRunAuthLoginSelectsBrowserForCustomPath(t *testing.T) {
	path := "/profiles/custom/Default"
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: path}
	source := &workflowCookieSource{
		profiles: []session.Profile{profile, {Browser: "chromium", Name: "Default", Path: path}},
		cookies:  map[string][]*http.Cookie{},
	}
	launcher := &workflowBrowserLauncher{onLaunch: func(session.Profile) {
		source.cookies[path] = []*http.Cookie{{Name: "EURIA_SESSION", Value: "fresh", Path: "/"}}
	}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("login request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		if _, err := r.Cookie("EURIA_SESSION"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--chrome-profile", path, "--browser", "chrome"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(launcher.profiles) != 1 || launcher.profiles[0].Browser != "chrome" || stdout.String() != "Euria session: signed in\n" {
		t.Fatalf("launched = %#v, stdout = %q; want Chrome profile and signed-in status", launcher.profiles, stdout.String())
	}
}

func TestRunAuthLoginReportsNoProfiles(t *testing.T) {
	launcher := &workflowBrowserLauncher{}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: &workflowCookieSource{cookies: map[string][]*http.Cookie{}}, BrowserLauncher: launcher,
	})
	if err == nil || !strings.Contains(err.Error(), "install or start Chrome or Chromium") || len(launcher.profiles) != 0 {
		t.Fatalf("Run() error = %v, launches = %d, want actionable missing-browser guidance without a launch", err, len(launcher.profiles))
	}
}

func TestRunAuthLoginReportsBrowserLaunchFailure(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	launcher := &workflowBrowserLauncher{err: errors.New("browser process failed")}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher,
	})
	if err == nil || !strings.Contains(err.Error(), "browser process failed") || stdout.Len() != 0 {
		t.Fatalf("Run() error = %v, stdout = %q, want launch failure", err, stdout.String())
	}
}

func TestRunAuthLoginTimesOutWaitingForSession(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{
		profile.Path: {{Name: "EURIA_SESSION", Value: "stale", Path: "/"}},
	}}
	launcher := &workflowBrowserLauncher{}
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("login request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: 15 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "euria auth status") {
		t.Fatalf("Run() error = %v, want actionable timeout", err)
	}
	if apiCalls < 2 || len(launcher.profiles) != 1 {
		t.Fatalf("auth status calls = %d, launches = %d, want polling after launch", apiCalls, len(launcher.profiles))
	}
}

func TestRunAuthLoginCancellationLeavesBrowserOpen(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &workflowBrowserLauncher{onLaunch: func(session.Profile) { cancel() }}
	var stdout, stderr bytes.Buffer
	err := Run(ctx, []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher, LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "browser remains open") {
		t.Fatalf("Run() error = %v, want cancellation guidance and open browser", err)
	}
	if len(launcher.profiles) != 1 {
		t.Fatalf("browser launches = %d, want one launch before cancellation", len(launcher.profiles))
	}
}

func TestRunDoesNotFallbackToGuest(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/test/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	guestCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/1/public/conversations/ephemeral" {
			guestCalls++
		}
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	want := "no signed-in Euria session found\n\n" +
		"  Sign in at https://euria.infomaniak.com in Chrome or Chromium.\n" +
		"  Check your session with: euria auth status\n" +
		"  Or use guest mode: euria --guest --chat \"YOUR MESSAGE\""
	if err == nil || err.Error() != want || guestCalls != 0 {
		t.Fatalf("Run() error = %v, guest calls = %d; want actionable auth guidance and no guest fallback", err, guestCalls)
	}
}

func TestRunRejectsMultipleSignedInProfiles(t *testing.T) {
	source := workflowSessionSource()
	second := session.Profile{Browser: "chromium", Name: "Profile 2", Path: "/test/chromium/Profile 2"}
	source.profiles = append(source.profiles, second)
	source.cookies[second.Path] = []*http.Cookie{{Name: "session", Value: "other", Path: "/"}}
	loginCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/1/login" {
			loginCalls++
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected request before profile selection: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "--chrome-profile") {
		t.Fatalf("Run() error = %v, want profile selection guidance", err)
	}
	if loginCalls != 2 {
		t.Fatalf("Run() validated %d profiles, want 2 read-only login checks", loginCalls)
	}
}

func TestRunIgnoresLoggedOutCookieProfiles(t *testing.T) {
	validProfile := session.Profile{Browser: "chrome", Name: "Default", Path: "/test/chrome/Default"}
	loggedOutProfile := session.Profile{Browser: "chromium", Name: "Profile 2", Path: "/test/chromium/Profile 2"}
	source := &workflowCookieSource{
		profiles: []session.Profile{loggedOutProfile, validProfile},
		cookies: map[string][]*http.Cookie{
			loggedOutProfile.Path: {{Name: "EURIA-API-XSRF-TOKEN", Value: "stale-xsrf", Path: "/"}},
			validProfile.Path:     {{Name: "EURIA_SESSION", Value: "valid-session", Path: "/"}},
		},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/1/login":
			if cookie, _ := r.Cookie("EURIA_SESSION"); cookie == nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/api/1/users/me/accounts":
			_, _ = io.WriteString(w, `[{"id":42}]`)
		case "/api/1/accounts/42/conversations":
			_, _ = io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: </stream>")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello"}, strings.NewReader(""), &stdout, io.Discard, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "answer\n" {
		t.Fatalf("Run() output = %q, want the valid profile's answer", stdout.String())
	}
}

func TestRunGuestSkipsCookieSource(t *testing.T) {
	source := &workflowCookieSource{err: errors.New("cookie source must not be touched")}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/1/public/conversations/ephemeral" {
			t.Errorf("guest request path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"guest answer\"}}]}\n\ndata: </stream>")
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--guest", "--chat", "hello"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if source.profileCalls != 0 || stdout.String() != "guest answer\n" {
		t.Fatalf("profile calls = %d, output = %q; want zero calls and answer-only text", source.profileCalls, stdout.String())
	}
}

func TestRunAuthStatusDoesNotPrintAccountData(t *testing.T) {
	source := workflowSessionSource()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/1/login" {
			t.Errorf("auth status request path = %q, want /api/1/login", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"email":"private@example.com","account_id":42}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "Euria session: signed in\n" || strings.Contains(stdout.String(), "private@example.com") || strings.Contains(stdout.String(), "42") {
		t.Fatalf("auth status output = %q, want only signed-in state", stdout.String())
	}
}

func TestRunAuthStatusReportsSignedOut(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/test/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "Euria session: not signed in\n" +
		"Sign in at https://euria.infomaniak.com in Chrome or Chromium.\n"
	if stdout.String() != want || apiCalls != 0 {
		t.Fatalf("auth status output = %q, API calls = %d; want actionable signed-out state and no request", stdout.String(), apiCalls)
	}
}

func TestRunRedactsCookieAndXsrfValues(t *testing.T) {
	source := workflowSessionSource()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/1/users/me/accounts" {
			_, _ = io.WriteString(w, `[{"id":42}]`)
			return
		}
		http.Error(w, "slow down session-cookie-secret xsrf-token-secret", http.StatusTooManyRequests)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err == nil || strings.Contains(err.Error(), "session-cookie-secret") || strings.Contains(err.Error(), "xsrf-token-secret") {
		t.Fatalf("Run() error = %v, want redacted request secrets", err)
	}
}

func TestRunJsonIncludesConversationID(t *testing.T) {
	source := workflowSessionSource()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/1/users/me/accounts" {
			_, _ = io.WriteString(w, `[{"id":42}]`)
			return
		}
		_, _ = io.WriteString(w, "data: {\"id\":\"conv-json\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: </stream>")
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--chat", "hello", "--output", "json"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"conversation_id":"conv-json"`) {
		t.Fatalf("JSON output = %q, want conversation_id", stdout.String())
	}
}
