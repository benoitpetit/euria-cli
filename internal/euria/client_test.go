package euria

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/benoitpetit/euria_retro/internal/session"
)

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client, server
}

func testSession() session.Session {
	return session.Session{
		Cookies: []*http.Cookie{
			{Name: "EURIA_SESSION", Value: "session-cookie-secret", Path: "/"},
			{Name: "EURIA-API-XSRF-TOKEN", Value: "xsrf-token-secret", Path: "/"},
		},
		XsrfToken: "xsrf-token-secret",
	}
}

func TestAuthStatusUsesSessionCookies(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/1/login" {
			t.Errorf("request = %s %s, want GET /api/1/login", r.Method, r.URL.Path)
		}
		cookie, err := r.Cookie("EURIA_SESSION")
		if err != nil || cookie.Value != "session-cookie-secret" {
			t.Errorf("request is missing the in-memory session cookie")
		}
		if got := r.Header.Get("X-XSRF-TOKEN"); got != "xsrf-token-secret" {
			t.Errorf("X-XSRF-TOKEN = %q, want session token", got)
		}
		w.WriteHeader(http.StatusOK)
	}))

	got, err := client.AuthStatus(context.Background(), testSession())
	if err != nil {
		t.Fatalf("AuthStatus() error = %v", err)
	}
	if !got {
		t.Fatal("AuthStatus() = false, want signed in")
	}
}

func TestAuthStatusFollowsSameOriginRedirect(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/1/login" {
			http.Redirect(w, r, "/signed-in", http.StatusFound)
			return
		}
		if r.URL.Path != "/signed-in" {
			t.Errorf("redirected request path = %q, want /signed-in", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	got, err := client.AuthStatus(context.Background(), testSession())
	if err != nil || !got {
		t.Fatalf("AuthStatus() = %v, %v; want signed in", got, err)
	}
}

func TestAuthenticatedPostSendsXsrfHeader(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/1/accounts/42/conversations" {
			t.Errorf("request = %s %s, want authenticated conversation POST", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-XSRF-TOKEN"); got != "xsrf-token-secret" {
			t.Errorf("X-XSRF-TOKEN = %q, want session token", got)
		}
		if _, err := r.Cookie("EURIA_SESSION"); err != nil {
			t.Errorf("request is missing session cookie")
		}
		w.WriteHeader(http.StatusOK)
	}))

	response, err := client.CreateConversation(context.Background(), testSession(), "42", ChatRequest{
		Message: "hello",
		Model:   "fast",
	})
	if err != nil {
		t.Fatalf("CreateConversation() error = %v", err)
	}
	response.Close()
}

func TestGuestConversationSendsNoCredentials(t *testing.T) {
	client, server := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/1/public/conversations/ephemeral" {
			t.Errorf("request = %s %s, want guest conversation POST", r.Method, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-XSRF-TOKEN") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("guest request sent browser credentials")
		}
		w.WriteHeader(http.StatusOK)
	}))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(mustURL(t, server.URL), []*http.Cookie{{Name: "unrelated", Value: "should-not-send", Path: "/"}})
	client.httpClient.Jar = jar

	response, err := client.GuestConversation(context.Background(), GuestRequest{
		Messages: []HistoryMessage{{Role: "user", Content: "hello"}},
		Model:    "fast",
		Tools:    []string{},
	})
	if err != nil {
		t.Fatalf("GuestConversation() error = %v", err)
	}
	response.Close()
}

func TestCreateAndContinueConversationUseObservedPayloads(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/1/accounts/42/conversations":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			if body["content"] != "first prompt" || body["model"] != "expert" || body["thinking"] != false {
				t.Errorf("create payload = %#v", body)
			}
			if got, ok := body["tools"].([]any); !ok || len(got) != 1 || got[0] != "web_search" {
				t.Errorf("create tools = %#v, want [web_search]", body["tools"])
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/1/accounts/42/conversations/conv-1/messages":
			_, _ = io.WriteString(w, `[{"id":"parent-1","parent_id":"older-1","role":"assistant","content":"previous answer"}]`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/1/accounts/42/conversations/conv-1/messages":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode continuation payload: %v", err)
			}
			if body["parent_id"] != "parent-1" || body["content"] != "follow-up" {
				t.Errorf("continuation payload = %#v", body)
			}
			if _, ok := body["model"]; ok {
				t.Errorf("continuation payload unexpectedly includes model: %#v", body)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
		w.WriteHeader(http.StatusOK)
	}))
	ctx := context.Background()
	userSession := testSession()

	created, err := client.CreateConversation(ctx, userSession, "42", ChatRequest{
		Message:  "first prompt",
		Model:    "expert",
		Tools:    []string{"web_search"},
		Thinking: pointer(false),
	})
	if err != nil {
		t.Fatalf("CreateConversation() error = %v", err)
	}
	created.Close()

	messages, err := client.ConversationMessages(ctx, userSession, "42", "conv-1")
	if err != nil {
		t.Fatalf("ConversationMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "parent-1" || messages[0].ParentID != "older-1" {
		t.Fatalf("ConversationMessages() = %#v", messages)
	}

	continued, err := client.ContinueConversation(ctx, userSession, "42", "conv-1", messages[0].ID, ChatRequest{Message: "follow-up", Tools: []string{"web_search_forced"}})
	if err != nil {
		t.Fatalf("ContinueConversation() error = %v", err)
	}
	continued.Close()
}

func TestGuestQuotaErrorExplainsLimit(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"quota"}`, http.StatusPaymentRequired)
	}))
	_, err := client.GuestConversation(context.Background(), GuestRequest{Model: "fast"})
	if err == nil || !strings.Contains(err.Error(), "5 messages per day") || !strings.Contains(err.Error(), "euria.infomaniak.com") {
		t.Fatalf("GuestConversation() error = %v, want guest quota guidance", err)
	}
}

func TestRateLimitErrorDoesNotExposeServerMessage(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private@example.com", http.StatusTooManyRequests)
	}))
	_, err := client.GuestConversation(context.Background(), GuestRequest{Model: "fast"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("GuestConversation() error = %v, want rate-limit status", err)
	}
	if strings.Contains(err.Error(), "private@example.com") {
		t.Fatalf("GuestConversation() error disclosed the response body: %v", err)
	}
}

func TestAuthStatusTreatsCrossOriginLoginRedirectAsSignedOut(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destination.Close)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", destination.URL+"/collect")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	client, err := NewClient(origin.URL, origin.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	signedIn, err := client.AuthStatus(context.Background(), testSession())
	if err != nil || signedIn {
		t.Fatalf("AuthStatus() = %v, %v; want signed out without following redirect", signedIn, err)
	}
	if got := destinationCalls.Load(); got != 0 {
		t.Fatalf("cross-origin destination received %d requests, want 0", got)
	}
}

func TestAuthenticatedPostIsNotReplayedBySameOriginRedirect(t *testing.T) {
	var posts atomic.Int32
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		http.Redirect(w, r, r.URL.Path+"?next=session-cookie-secret", http.StatusTemporaryRedirect)
	}))
	_, err := client.CreateConversation(context.Background(), testSession(), "42", ChatRequest{Message: "hello", Model: "fast"})
	if err == nil {
		t.Fatal("CreateConversation() followed a same-origin redirect")
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("chat POST attempts = %d, want 1", got)
	}
	if strings.Contains(err.Error(), "session-cookie-secret") {
		t.Fatalf("redirect error exposed a session value: %v", err)
	}
}

func TestAuthenticatedHTTPErrorDoesNotExposeResponseBody(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private@example.com session-cookie-secret", http.StatusInternalServerError)
	}))
	_, err := client.Accounts(context.Background(), testSession())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("Accounts() error = %v, want HTTP 500", err)
	}
	if strings.Contains(err.Error(), "private@example.com") || strings.Contains(err.Error(), "session-cookie-secret") {
		t.Fatalf("Accounts() error disclosed response data: %v", err)
	}
}

func TestTransportErrorDoesNotExposeRequestURL(t *testing.T) {
	client, err := NewClient(defaultOrigin, &http.Client{Transport: failingRoundTripper{err: errors.New("dial failed")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateConversation(context.Background(), testSession(), "private-account-id", ChatRequest{Message: "hello", Model: "fast"})
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("CreateConversation() error = %v, want a generic request failure", err)
	}
	if strings.Contains(err.Error(), "private-account-id") || strings.Contains(err.Error(), "euria.infomaniak.com") {
		t.Fatalf("transport error disclosed request URL: %v", err)
	}
}

func TestSessionCookieAndXsrfUpdatesPersistBetweenRequests(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/1/users/me/accounts":
			http.SetCookie(w, &http.Cookie{Name: "EURIA_SESSION", Value: "rotated-session", Path: "/", Secure: true})
			http.SetCookie(w, &http.Cookie{Name: "EURIA-API-XSRF-TOKEN", Value: "rotated-xsrf", Path: "/", Secure: true})
			_, _ = io.WriteString(w, `[{"id":42}]`)
		case "/api/1/accounts/42/conversations":
			cookie, err := r.Cookie("EURIA_SESSION")
			if err != nil || cookie.Value != "rotated-session" {
				t.Errorf("conversation cookie = %v, want rotated session", cookie)
			}
			if got := r.Header.Get("X-XSRF-TOKEN"); got != "rotated-xsrf" {
				t.Errorf("X-XSRF-TOKEN = %q, want rotated-xsrf", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	userSession := testSession()
	if _, err := client.Accounts(context.Background(), userSession); err != nil {
		t.Fatalf("Accounts() error = %v", err)
	}
	response, err := client.CreateConversation(context.Background(), userSession, "42", ChatRequest{Message: "hello", Model: "fast"})
	if err != nil {
		t.Fatalf("CreateConversation() error = %v", err)
	}
	response.Close()
}

type failingRoundTripper struct{ err error }

func (r failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

func TestChatPostIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("test server does not support hijacking")
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		connection.Close()
	}))
	_, _ = client.CreateConversation(context.Background(), testSession(), "42", ChatRequest{Message: "hello", Model: "fast"})
	if got := calls.Load(); got != 1 {
		t.Fatalf("POST attempts = %d, want exactly 1", got)
	}
}

func TestClientRejectsHTTPOrigin(t *testing.T) {
	if _, err := NewClient("http://euria.infomaniak.com", nil); err == nil {
		t.Fatal("NewClient() accepted a non-HTTPS origin")
	}
}

func TestClientRejectsNonstandardPortForCaseVariantEuriaHost(t *testing.T) {
	if _, err := NewClient("https://EURIA.Infomaniak.com:8443", nil); err == nil {
		t.Fatal("NewClient() accepted a nonstandard port for a case-variant Euria host")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func pointer[T any](value T) *T { return &value }
