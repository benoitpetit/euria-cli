package euria

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/benoitpetit/euria-cli/internal/session"
)

const (
	defaultOrigin  = "https://euria.infomaniak.com"
	requestTimeout = 5 * time.Minute
)

// Client sends requests to Euria's web API.
type Client struct {
	origin     *url.URL
	httpClient *http.Client
	jarMu      sync.Mutex
	jarByKey   map[[32]byte]*cookiejar.Jar
}

// ChatRequest describes a new or continued Euria message.
type ChatRequest struct {
	Message  string
	Model    string
	Tools    []string
	Thinking *bool
}

// HistoryMessage is one user or assistant turn sent to the guest endpoint.
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GuestRequest contains the messages and options sent to the public endpoint.
type GuestRequest struct {
	Messages []HistoryMessage
	Model    string
	Tools    []string
	Thinking *bool
}

type conversationPayload struct {
	ProjectID string   `json:"project_id,omitempty"`
	Content   string   `json:"content"`
	FileIDs   []string `json:"file_ids"`
	Tools     []string `json:"tools"`
	Model     string   `json:"model,omitempty"`
	Thinking  *bool    `json:"thinking,omitempty"`
}

type continuationPayload struct {
	ParentID string   `json:"parent_id"`
	Content  string   `json:"content"`
	FileIDs  []string `json:"file_ids"`
	Tools    []string `json:"tools"`
	Thinking *bool    `json:"thinking,omitempty"`
}

type guestPayload struct {
	Messages []HistoryMessage `json:"messages"`
	Model    string           `json:"model"`
	Tools    []string         `json:"tools"`
	Thinking *bool            `json:"thinking,omitempty"`
}

// NewClient creates a client for the Euria HTTPS origin. Loopback HTTPS origins
// are accepted for local tests; other hosts must be the production Euria host.
func NewClient(origin string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Euria API origin must be an HTTPS origin without credentials, path, query, or fragment")
	}
	if !isAllowedHost(parsed) {
		return nil, errors.New("Euria API origin must use euria.infomaniak.com")
	}
	if strings.EqualFold(parsed.Hostname(), "euria.infomaniak.com") && parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("Euria API origin must use the standard HTTPS port")
	}

	base := &http.Client{Timeout: requestTimeout}
	if httpClient != nil {
		copy := *httpClient
		base = &copy
		if base.Timeout <= 0 {
			base.Timeout = requestTimeout
		}
	}
	base.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || !sameOrigin(parsed, request.URL) || len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		method := via[0].Method
		if method != http.MethodGet && method != http.MethodHead {
			return http.ErrUseLastResponse
		}
		return nil
	}

	return &Client{origin: parsed, httpClient: base, jarByKey: make(map[[32]byte]*cookiejar.Jar)}, nil
}

// AuthStatus checks whether Euria accepts the browser session.
func (c *Client) AuthStatus(ctx context.Context, userSession session.Session) (bool, error) {
	response, err := c.send(ctx, http.MethodGet, "/api/1/users/me", nil, &userSession)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == 419 ||
		(response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest) {
		return false, nil
	}
	if !isSuccess(response.StatusCode) {
		return false, responseError(response, false)
	}
	return true, nil
}

// CreateConversation starts a saved Euria account conversation.
func (c *Client) CreateConversation(ctx context.Context, userSession session.Session, accountID, projectID string, request ChatRequest) (io.ReadCloser, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	payload := conversationPayload{
		ProjectID: projectID,
		Content:   request.Message,
		FileIDs:   []string{},
		Tools:     nonNilStrings(request.Tools),
		Model:     request.Model,
		Thinking:  request.Thinking,
	}
	response, err := c.send(ctx, http.MethodPost, "/api/1/accounts/"+accountID+"/conversations", payload, &userSession)
	if err != nil {
		return nil, err
	}
	if !isSuccess(response.StatusCode) {
		defer response.Body.Close()
		return nil, responseError(response, false)
	}
	return response.Body, nil
}

// ContinueConversation sends a message to an existing saved conversation.
func (c *Client) ContinueConversation(ctx context.Context, userSession session.Session, accountID, conversationID, parentID string, request ChatRequest) (io.ReadCloser, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	conversationID, err = pathSegment(conversationID, "conversation")
	if err != nil {
		return nil, err
	}
	if parentID, err = pathSegment(parentID, "parent message"); err != nil {
		return nil, err
	}
	payload := continuationPayload{
		ParentID: parentID,
		Content:  request.Message,
		FileIDs:  []string{},
		Tools:    nonNilStrings(request.Tools),
		Thinking: request.Thinking,
	}
	path := "/api/1/accounts/" + accountID + "/conversations/" + conversationID + "/messages"
	response, err := c.send(ctx, http.MethodPost, path, payload, &userSession)
	if err != nil {
		return nil, err
	}
	if !isSuccess(response.StatusCode) {
		defer response.Body.Close()
		return nil, responseError(response, false)
	}
	return response.Body, nil
}

// EphemeralConversation sends an ephemeral prompt for a signed-in account without
// saving a conversation.
func (c *Client) EphemeralConversation(ctx context.Context, userSession session.Session, accountID string, request GuestRequest) (io.ReadCloser, error) {
	accountID, err := pathSegment(accountID, "account")
	if err != nil {
		return nil, err
	}
	model := request.Model
	if model == "" {
		model = "fast"
	}
	messages := request.Messages
	if messages == nil {
		messages = []HistoryMessage{}
	}
	payload := struct {
		Messages []HistoryMessage `json:"messages"`
		Model    string           `json:"model"`
		Tools    []string         `json:"tools"`
		Thinking *bool            `json:"thinking,omitempty"`
	}{
		Messages: messages,
		Model:    model,
		Tools:    nonNilStrings(request.Tools),
		Thinking: request.Thinking,
	}
	response, err := c.send(ctx, http.MethodPost, "/api/1/accounts/"+accountID+"/conversations/ephemeral", payload, &userSession)
	if err != nil {
		return nil, err
	}
	if !isSuccess(response.StatusCode) {
		defer response.Body.Close()
		return nil, responseError(response, false)
	}
	return response.Body, nil
}

// GuestConversation sends an ephemeral prompt without browser credentials.
func (c *Client) GuestConversation(ctx context.Context, request GuestRequest) (io.ReadCloser, error) {
	model := request.Model
	if model == "" || model == "expert" {
		model = "fast"
	}
	payload := guestPayload{Messages: request.Messages, Model: model, Tools: nonNilStrings(request.Tools), Thinking: request.Thinking}
	response, err := c.send(ctx, http.MethodPost, "/api/1/public/conversations/ephemeral", payload, nil)
	if err != nil {
		return nil, err
	}
	if !isSuccess(response.StatusCode) {
		defer response.Body.Close()
		return nil, responseError(response, true)
	}
	return response.Body, nil
}

func (c *Client) send(ctx context.Context, method, endpoint string, payload any, userSession *session.Session) (*http.Response, error) {
	target := *c.origin
	target.Path = endpoint
	target.RawPath = ""
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode Euria request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create Euria request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream, application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.requestClient(userSession)
	if userSession != nil {
		if token := xsrfCookie(client.Jar, c.origin, userSession.XsrfToken); token != "" {
			request.Header.Set("X-XSRF-TOKEN", token)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Euria request failed: %w", ctx.Err())
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, errors.New("Euria request timed out")
		}
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return nil, errors.New("Euria request failed (DNS resolution error)")
		}
		return nil, errors.New("Euria request failed (network or TLS error)")
	}
	return response, nil
}

func (c *Client) requestClient(userSession *session.Session) *http.Client {
	client := *c.httpClient
	client.Jar = nil
	if userSession != nil {
		client.Jar = c.sessionJar(userSession)
	}
	return &client
}

func (c *Client) sessionJar(userSession *session.Session) *cookiejar.Jar {
	key := sessionKey(userSession)
	c.jarMu.Lock()
	defer c.jarMu.Unlock()
	if jar := c.jarByKey[key]; jar != nil {
		return jar
	}
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(c.origin, originScopedCookies(userSession.Cookies, c.origin))
	c.jarByKey[key] = jar
	return jar
}

func sessionKey(userSession *session.Session) [32]byte {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", userSession.Profile.Browser, userSession.Profile.Path, userSession.XsrfToken)
	for _, cookie := range userSession.Cookies {
		if cookie != nil {
			_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00", cookie.Name, cookie.Domain, cookie.Path, cookie.Value)
		}
	}
	var key [32]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func xsrfCookie(jar http.CookieJar, origin *url.URL, fallback string) string {
	if jar != nil {
		for _, cookie := range jar.Cookies(origin) {
			if cookie.Name == "EURIA-API-XSRF-TOKEN" {
				return cookie.Value
			}
		}
	}
	return fallback
}

func originScopedCookies(cookies []*http.Cookie, origin *url.URL) []*http.Cookie {
	filtered := make([]*http.Cookie, 0, len(cookies))
	host := strings.ToLower(origin.Hostname())
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
		if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		copy := *cookie
		filtered = append(filtered, &copy)
	}
	return filtered
}

func responseError(response *http.Response, guest bool) error {
	if guest && response.StatusCode == http.StatusPaymentRequired {
		return errors.New("Euria guest quota reached (5 messages per day). Sign in at https://euria.infomaniak.com or wait for the quota reset")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == 419 {
		return errors.New("Euria session expired or signed out; sign in through Chrome/Chromium and retry")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return errors.New("Euria rate limit reached (HTTP 429)")
	}
	message := serverErrorMessage(response)
	if message != "" {
		return fmt.Errorf("Euria returned HTTP %d: %s", response.StatusCode, message)
	}
	return fmt.Errorf("Euria returned HTTP %d", response.StatusCode)
}

// serverErrorMessage best-effort extracts a human-readable message from an
// error response body without leaking cookie values.
func serverErrorMessage(response *http.Response) string {
	if response == nil || response.Body == nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		for _, key := range []string{"error", "message", "detail", "description"} {
			if value, ok := parsed[key]; ok {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
				if nested, ok := value.(map[string]any); ok {
					if text, ok := nested["message"].(string); ok && strings.TrimSpace(text) != "" {
						return strings.TrimSpace(text)
					}
				}
			}
		}
	}
	return ""
}

func isSuccess(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func isAllowedHost(origin *url.URL) bool {
	if strings.EqualFold(origin.Hostname(), "euria.infomaniak.com") || strings.EqualFold(origin.Hostname(), "localhost") {
		return true
	}
	address := net.ParseIP(origin.Hostname())
	return address != nil && address.IsLoopback()
}

func sameOrigin(expected, actual *url.URL) bool {
	return strings.EqualFold(expected.Scheme, actual.Scheme) &&
		strings.EqualFold(expected.Hostname(), actual.Hostname()) && effectivePort(expected) == effectivePort(actual)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

func pathSegment(value, label string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s ID is required", label)
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return "", fmt.Errorf("%s ID contains an invalid path character", label)
		}
	}
	return value, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
