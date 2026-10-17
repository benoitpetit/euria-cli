package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benoitpetit/euria_retro/internal/session"
)

type workflowCookieSource struct {
	profiles       []session.Profile
	cookies        map[string][]*http.Cookie
	profileErrors  map[string]error
	warnings       []string
	err            error
	discoveryCalls int
	options        []session.DiscoveryOptions
}

func (s *workflowCookieSource) Discover(_ context.Context, options session.DiscoveryOptions) (session.DiscoveryResult, error) {
	s.discoveryCalls++
	s.options = append(s.options, options)
	if s.err != nil {
		return session.DiscoveryResult{}, s.err
	}
	result := session.DiscoveryResult{Warnings: append([]string(nil), s.warnings...)}
	for _, profile := range s.profiles {
		if len(options.Browsers) > 0 && !containsBrowser(options.Browsers, profile.Browser) {
			continue
		}
		if selected, ok := options.Profiles[profile.Browser]; ok && selected != profile.Name && filepath.Clean(selected) != filepath.Clean(profile.Path) {
			continue
		}
		if err := s.profileErrors[profile.Path]; err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		cookies := append([]*http.Cookie(nil), s.cookies[profile.Path]...)
		if len(cookies) == 0 {
			continue
		}
		userSession := session.Session{Profile: profile, Cookies: cookies}
		for _, cookie := range cookies {
			if cookie.Name == "EURIA-API-XSRF-TOKEN" {
				userSession.XsrfToken = cookie.Value
			}
		}
		result.Sessions = append(result.Sessions, userSession)
	}
	return result, nil
}

func containsBrowser(browsers []session.Browser, browser session.Browser) bool {
	for _, candidate := range browsers {
		if candidate == browser {
			return true
		}
	}
	return false
}

type workflowBrowserLauncher struct {
	targets []string
	err     error
	onOpen  func()
}

func (l *workflowBrowserLauncher) Open(target string) error {
	l.targets = append(l.targets, target)
	if l.onOpen != nil {
		l.onOpen()
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

func TestDiscoverSessionsUsesSelectedBrowsersAndProfiles(t *testing.T) {
	selected := session.Profile{Browser: "firefox", Name: "work", Path: "/firefox/work/cookies.sqlite"}
	other := session.Profile{Browser: "chrome", Name: "Default", Path: "/chrome/Default/Cookies"}
	source := &workflowCookieSource{
		profiles: []session.Profile{other, selected},
		cookies: map[string][]*http.Cookie{
			selected.Path: {{Name: "EURIA_SESSION", Value: "selected", Path: "/"}},
			other.Path:    {{Name: "EURIA_SESSION", Value: "not-selected", Path: "/"}},
		},
	}
	opts := Options{Browsers: []session.Browser{"firefox"}, Profile: "work"}
	result, err := discoverSessions(context.Background(), opts, Dependencies{CookieSource: source})
	if err != nil {
		t.Fatalf("discoverSessions() error = %v", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Profile.Path != selected.Path {
		t.Fatalf("discoverSessions() sessions = %#v, want selected Firefox profile", result.Sessions)
	}
	if len(source.options) != 1 || source.options[0].Profiles[session.Browser("firefox")] != "work" {
		t.Fatalf("source options = %#v, want Firefox profile work", source.options)
	}
}

func TestDiscoverSessionsKeepsValidBrowserWhenAnotherSourceFails(t *testing.T) {
	locked := session.Profile{Browser: "chrome", Name: "Default", Path: "/chrome/Default/Cookies"}
	usable := session.Profile{Browser: "firefox", Name: "default", Path: "/firefox/default/cookies.sqlite"}
	source := &workflowCookieSource{
		profiles:      []session.Profile{locked, usable},
		profileErrors: map[string]error{locked.Path: errors.New("browser database is locked")},
		cookies:       map[string][]*http.Cookie{usable.Path: {{Name: "EURIA_SESSION", Value: "usable-session", Path: "/"}}},
	}
	result, err := discoverSessions(context.Background(), Options{}, Dependencies{CookieSource: source})
	if err != nil {
		t.Fatalf("discoverSessions() error = %v, want usable browser session", err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].Profile.Browser != "firefox" {
		t.Fatalf("discoverSessions() sessions = %#v, want Firefox session", result.Sessions)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "locked") {
		t.Fatalf("discoverSessions() warnings = %v, want locked-source diagnostic", result.Warnings)
	}
}

func TestAuthLoginOpensSystemURLAndDetectsSession(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/chrome/Default/Cookies"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	launcher := &workflowBrowserLauncher{onOpen: func() {
		source.cookies[profile.Path] = []*http.Cookie{{Name: "EURIA_SESSION", Value: "new-session", Path: "/"}}
	}}
	apiCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("auth check = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		if _, err := r.Cookie("EURIA_SESSION"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--browser", "chrome"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(launcher.targets) != 1 || launcher.targets[0] != euriaLoginURL {
		t.Fatalf("browser targets = %v, want %q", launcher.targets, euriaLoginURL)
	}
	if stdout.String() != "Euria session: signed in\n" || apiCalls != 1 {
		t.Fatalf("stdout = %q, API calls = %d; want signed-in status after one local check", stdout.String(), apiCalls)
	}
}

func TestAuthLoginReportsOpenerFailure(t *testing.T) {
	launcher := &workflowBrowserLauncher{err: errors.New("system opener failed")}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: &workflowCookieSource{}, BrowserLauncher: launcher,
	})
	if err == nil || !strings.Contains(err.Error(), "system opener failed") || stdout.Len() != 0 {
		t.Fatalf("Run() error = %v, stdout = %q; want opener failure", err, stdout.String())
	}
}

func TestAuthLoginCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &workflowBrowserLauncher{onOpen: cancel}
	var stdout, stderr bytes.Buffer
	err := Run(ctx, []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: &workflowCookieSource{}, BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "browser remains open") {
		t.Fatalf("Run() error = %v, want cancellation guidance", err)
	}
	if len(launcher.targets) != 1 || launcher.targets[0] != euriaLoginURL {
		t.Fatalf("browser targets = %v, want one Euria login URL", launcher.targets)
	}
}

func TestAuthLoginTimeout(t *testing.T) {
	launcher := &workflowBrowserLauncher{}
	profile := session.Profile{Browser: "firefox", Name: "work", Path: "/firefox/work/cookies.sqlite"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, warnings: []string{"Firefox database is locked"}}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "login", "--browser", "firefox", "--profile", "work"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: 5 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "Firefox database is locked") || !strings.Contains(err.Error(), "--browser firefox --profile work") {
		t.Fatalf("Run() error = %v, want timeout, latest source warning, and selected auth status command", err)
	}
	if len(launcher.targets) != 1 || launcher.targets[0] != euriaLoginURL {
		t.Fatalf("browser targets = %v, want one Euria login URL", launcher.targets)
	}
	if !strings.Contains(stderr.String(), "firefox") || !strings.Contains(stderr.String(), "profile work") || !strings.Contains(stderr.String(), "default browser may differ") {
		t.Fatalf("login guidance = %q, want selected browser/profile and opener mismatch guidance", stderr.String())
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
	if guestCalls != 0 || source.discoveryCalls != 1 {
		t.Fatalf("guest calls = %d, session discovery calls = %d; want 0 and 1", guestCalls, source.discoveryCalls)
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
	if stdout.String() != "Euria session: signed in\n" || len(launcher.targets) != 0 || apiCalls != 1 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, launches = %d, API calls = %d", stdout.String(), stderr.String(), len(launcher.targets), apiCalls)
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
	profile := session.Profile{Browser: "firefox", Name: "work", Path: "/profiles/firefox/work"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, warnings: []string{"Firefox browser database is locked"}, cookies: map[string][]*http.Cookie{
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
	err := Run(context.Background(), []string{"auth", "login", "--browser", "firefox", "--profile", "work"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		EuriaOrigin: server.URL, CookieSource: source, HTTPClient: server.Client(), BrowserLauncher: launcher,
		LoginPollInterval: time.Millisecond, LoginTimeout: 15 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "euria auth status --browser firefox --profile work") || !strings.Contains(err.Error(), "Firefox browser database is locked") {
		t.Fatalf("Run() error = %v, want actionable timeout", err)
	}
	if !strings.Contains(stderr.String(), "profile work") || !strings.Contains(stderr.String(), "default browser may differ") {
		t.Fatalf("login guidance = %q, want selected source and browser-opener guidance", stderr.String())
	}
	if apiCalls < 2 || len(launcher.targets) != 1 {
		t.Fatalf("auth status calls = %d, launches = %d, want polling after launch", apiCalls, len(launcher.targets))
	}
}

func TestRunAuthLoginCancellationLeavesBrowserOpen(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/profiles/chrome/Default"}
	source := &workflowCookieSource{profiles: []session.Profile{profile}, cookies: map[string][]*http.Cookie{}}
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &workflowBrowserLauncher{onOpen: cancel}
	var stdout, stderr bytes.Buffer
	err := Run(ctx, []string{"auth", "login"}, strings.NewReader(""), &stdout, &stderr, Dependencies{
		CookieSource: source, BrowserLauncher: launcher, LoginPollInterval: time.Millisecond, LoginTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "browser remains open") {
		t.Fatalf("Run() error = %v, want cancellation guidance and open browser", err)
	}
	if len(launcher.targets) != 1 {
		t.Fatalf("browser launches = %d, want one launch before cancellation", len(launcher.targets))
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
		"  Sign in at https://euria.infomaniak.com in your browser.\n" +
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
	if err == nil || !strings.Contains(err.Error(), "--browser NAME") {
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
	if source.discoveryCalls != 0 || stdout.String() != "guest answer\n" {
		t.Fatalf("session discovery calls = %d, output = %q; want zero calls and answer-only text", source.discoveryCalls, stdout.String())
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
		"Sign in at https://euria.infomaniak.com in your browser.\n"
	if stdout.String() != want || apiCalls != 0 {
		t.Fatalf("auth status output = %q, API calls = %d; want actionable signed-out state and no request", stdout.String(), apiCalls)
	}
}

func TestRunAuthStatusSuppressesAutomaticMissingBrowserWarnings(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/chrome/Default/Cookies"}
	source := &workflowCookieSource{
		profiles:      []session.Profile{profile},
		profileErrors: map[string]error{profile.Path: errors.New("chrome cookie store not found")},
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, Dependencies{CookieSource: source})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != "Euria session: not signed in\n"+signInInstruction+"\n" || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q; want concise signed-out guidance without automatic-source noise", stdout.String(), stderr.String())
	}
}

func TestRunAuthStatusReportsAutomaticActionableWarningsWithoutSession(t *testing.T) {
	source := &workflowCookieSource{warnings: []string{"Firefox browser database is locked; unlock the system keyring"}}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "status"}, strings.NewReader(""), &stdout, &stderr, Dependencies{CookieSource: source})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "database is locked") || stdout.String() != "Euria session: not signed in\n"+signInInstruction+"\n" {
		t.Fatalf("stdout = %q, stderr = %q; want actionable automatic-source warning", stdout.String(), stderr.String())
	}
}

func TestRunAuthStatusReportsSelectedBrowserWarnings(t *testing.T) {
	profile := session.Profile{Browser: "chrome", Name: "Default", Path: "/chrome/Default/Cookies"}
	source := &workflowCookieSource{
		profiles:      []session.Profile{profile},
		profileErrors: map[string]error{profile.Path: errors.New("browser database is locked")},
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "status", "--browser", "chrome"}, strings.NewReader(""), &stdout, &stderr, Dependencies{CookieSource: source})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "browser database is locked") || stdout.String() != "Euria session: not signed in\n"+signInInstruction+"\n" {
		t.Fatalf("stdout = %q, stderr = %q; want selected-source diagnostic and signed-out status", stdout.String(), stderr.String())
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
