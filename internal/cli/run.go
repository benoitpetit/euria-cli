package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/benoitpetit/euria_retro/internal/euria"
	"github.com/benoitpetit/euria_retro/internal/session"
)

const defaultEuriaOrigin = "https://euria.infomaniak.com"
const euriaLoginURL = "https://euria.infomaniak.com/api/1/login"

const (
	defaultLoginPollInterval = 2 * time.Second
	defaultLoginTimeout      = 5 * time.Minute
)

const signInInstruction = "Sign in at https://euria.infomaniak.com in Chrome or Chromium."

// Dependencies supplies the Euria origin and replaceable HTTP/browser sources.
type Dependencies struct {
	EuriaOrigin       string
	CookieSource      session.CookieSource
	HTTPClient        *http.Client
	BrowserLauncher   session.BrowserLauncher
	LoginPollInterval time.Duration
	LoginTimeout      time.Duration
}

// Run executes a parsed Euria CLI command.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, dependencies Dependencies) error {
	opts, err := Parse(args)
	if err != nil {
		return err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		if stdin == nil || isTerminal(stdin) {
			_, err := io.WriteString(stdout, Usage())
			return err
		}
		input, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read prompt from stdin: %w", err)
		}
		opts.Prompt = strings.TrimSpace(string(input))
		if opts.Prompt == "" {
			_, err := io.WriteString(stdout, Usage())
			return err
		}
		return runChat(ctx, opts, nil, stdout, dependencies)
	}
	switch opts.Command {
	case "help":
		_, err := io.WriteString(stdout, Usage())
		return err
	case "list-models":
		_, err := io.WriteString(stdout, "fast\nexpert\napertus\n")
		return err
	case "auth-status":
		return runAuthStatus(ctx, opts, stdout, dependencies)
	case "auth-login":
		return runAuthLogin(ctx, opts, stdout, stderr, dependencies)
	default:
		return runChat(ctx, opts, stdin, stdout, dependencies)
	}
}

func runAuthLogin(ctx context.Context, opts Options, stdout, stderr io.Writer, dependencies Dependencies) error {
	source := dependencies.CookieSource
	if source == nil {
		source = session.NewChromiumCookieSource()
	}
	source = selectBrowserSource(source, opts.Browser)
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}

	if opts.ChromeProfile == "" {
		sessions, discoverErr := session.Discover(ctx, source, "")
		if discoverErr != nil && !errors.Is(discoverErr, session.ErrNoSession) {
			return discoverErr
		}
		if discoverErr == nil {
			sessions, err = signedInSessions(ctx, client, sessions)
			if err != nil {
				return err
			}
			if len(sessions) > 1 {
				return requireProfileSelection("", sessionProfiles(sessions))
			}
			if len(sessions) == 1 {
				return writeSessionStatus(stdout, true)
			}
		}
	}

	profiles, err := source.Profiles(ctx, opts.ChromeProfile)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		if opts.ChromeProfile != "" {
			return fmt.Errorf("Chrome/Chromium profile %q was not found; pass a profile name or path", opts.ChromeProfile)
		}
		return errors.New("no Chrome/Chromium profile found; install or start Chrome or Chromium and retry")
	}
	if len(profiles) > 1 {
		return requireProfileSelection(opts.ChromeProfile, profiles)
	}
	profile := profiles[0]

	sessions, discoverErr := session.Discover(ctx, source, profile.Path)
	if discoverErr != nil && !errors.Is(discoverErr, session.ErrNoSession) {
		return discoverErr
	}
	if discoverErr == nil {
		sessions, err = signedInSessions(ctx, client, sessions)
		if err != nil {
			return err
		}
		if len(sessions) > 1 {
			return requireProfileSelection(opts.ChromeProfile, sessionProfiles(sessions))
		}
		if len(sessions) == 1 {
			return writeSessionStatus(stdout, true)
		}
	}

	launcher := dependencies.BrowserLauncher
	if launcher == nil {
		launcher = session.NewChromiumBrowserLauncher()
	}
	if err := launcher.Launch(profile, euriaLoginURL); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stderr, "Complete the Infomaniak sign-in in the browser. Euria will detect the session automatically; press Ctrl+C to cancel."); err != nil {
		return err
	}

	interval := dependencies.LoginPollInterval
	if interval <= 0 {
		interval = defaultLoginPollInterval
	}
	timeout := dependencies.LoginTimeout
	if timeout <= 0 {
		timeout = defaultLoginTimeout
	}
	if err := waitForLogin(ctx, client, source, profile, interval, timeout); err != nil {
		return err
	}
	return writeSessionStatus(stdout, true)
}

func sessionProfiles(sessions []session.Session) []session.Profile {
	profiles := make([]session.Profile, 0, len(sessions))
	for _, userSession := range sessions {
		profiles = append(profiles, userSession.Profile)
	}
	return profiles
}

func requireProfileSelection(selector string, profiles []session.Profile) error {
	if path, ok := samePathBrowserAmbiguity(profiles); ok {
		return fmt.Errorf("profile path %q could be Chrome or Chromium; specify --browser chrome or --browser chromium with --chrome-profile", path)
	}
	message := "multiple Chrome/Chromium profiles are available; choose one with --chrome-profile NAME|PATH:"
	if selector != "" {
		message = fmt.Sprintf("profile %q matches multiple Chrome/Chromium profiles; choose a full path with --chrome-profile:", selector)
	}
	for _, profile := range profiles {
		message += fmt.Sprintf("\n  %s: %s", profile.Name, profile.Path)
	}
	return errors.New(message)
}

func samePathBrowserAmbiguity(profiles []session.Profile) (string, bool) {
	if len(profiles) < 2 {
		return "", false
	}
	path := profiles[0].Path
	browser := profiles[0].Browser
	otherBrowser := false
	for _, profile := range profiles[1:] {
		if profile.Path != path {
			return "", false
		}
		if profile.Browser != browser {
			otherBrowser = true
		}
	}
	return path, otherBrowser
}

func waitForLogin(ctx context.Context, client *euria.Client, source session.CookieSource, profile session.Profile, interval, timeout time.Duration) error {
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := loginCtx.Err(); err != nil {
			return loginWaitError(err, profile)
		}
		sessions, err := session.Discover(loginCtx, source, profile.Path)
		if err != nil && !errors.Is(err, session.ErrNoSession) {
			if loginCtx.Err() != nil {
				return loginWaitError(loginCtx.Err(), profile)
			}
			return err
		}
		if err == nil {
			signedIn, err := signedInSessions(loginCtx, client, sessions)
			if err != nil {
				if loginCtx.Err() != nil {
					return loginWaitError(loginCtx.Err(), profile)
				}
				return err
			}
			if len(signedIn) != 0 {
				return nil
			}
		}
		select {
		case <-loginCtx.Done():
			return loginWaitError(loginCtx.Err(), profile)
		case <-ticker.C:
		}
	}
}

func loginWaitError(err error, profile session.Profile) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("login wait cancelled; the browser remains open")
	}
	return fmt.Errorf("timed out waiting for Euria sign-in; check the selected profile with: euria auth status --chrome-profile %q --browser %s", profile.Path, profile.Browser)
}

func runAuthStatus(ctx context.Context, opts Options, stdout io.Writer, dependencies Dependencies) error {
	sessions, err := discoverSessions(ctx, opts.ChromeProfile, opts.Browser, dependencies)
	if errors.Is(err, session.ErrNoSession) {
		return writeSessionStatus(stdout, false)
	}
	if err != nil {
		return err
	}
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	sessions, err = signedInSessions(ctx, client, sessions)
	if err != nil {
		return err
	}
	if len(sessions) > 1 {
		return requireProfileSelection(opts.ChromeProfile, sessionProfiles(sessions))
	}
	return writeSessionStatus(stdout, len(sessions) == 1)
}

func runChat(ctx context.Context, opts Options, stdin io.Reader, stdout io.Writer, dependencies Dependencies) error {
	prompt, err := ReadPrompt(opts, stdin)
	if err != nil {
		return err
	}
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	if opts.Guest {
		return runGuestChat(ctx, client, opts, prompt, stdout)
	}

	sessions, err := discoverSessions(ctx, opts.ChromeProfile, opts.Browser, dependencies)
	if err != nil {
		if errors.Is(err, session.ErrNoSession) {
			return noSignedInSessionError()
		}
		return err
	}
	sessions, err = signedInSessions(ctx, client, sessions)
	if err != nil {
		return err
	}
	userSession, err := selectSession(sessions)
	if err != nil {
		return err
	}
	accounts, err := client.Accounts(ctx, userSession)
	if err != nil {
		return err
	}
	account, err := euria.SelectAccount(accounts, opts.AccountID)
	if err != nil {
		return err
	}
	request := euria.ChatRequest{Message: prompt, Model: opts.Model, Tools: toolsForSearch(opts.WebSearch)}
	var stream io.ReadCloser
	if opts.ConversationID == "" {
		stream, err = client.CreateConversation(ctx, userSession, account.ID, request)
	} else {
		messages, messageErr := client.ConversationMessages(ctx, userSession, account.ID, opts.ConversationID)
		if messageErr != nil {
			return messageErr
		}
		if len(messages) == 0 || messages[len(messages)-1].ID == "" {
			return errors.New("the selected Euria conversation has no message to continue")
		}
		stream, err = client.ContinueConversation(ctx, userSession, account.ID, opts.ConversationID, messages[len(messages)-1].ID, request)
	}
	if err != nil {
		return err
	}
	defer stream.Close()
	result, err := euria.ReadStream(stream)
	if err != nil {
		return redactSessionError(err, userSession)
	}
	if result.ConversationID == "" {
		result.ConversationID = opts.ConversationID
	}
	return writeChatResult(stdout, opts.Output, result)
}

func runGuestChat(ctx context.Context, client *euria.Client, opts Options, prompt string, stdout io.Writer) error {
	messages, err := readHistory(opts.HistoryPath)
	if err != nil {
		return err
	}
	messages = append(messages, euria.HistoryMessage{Role: "user", Content: prompt})
	stream, err := client.GuestConversation(ctx, euria.GuestRequest{
		Messages: messages,
		Model:    opts.Model,
		Tools:    toolsForSearch(opts.WebSearch),
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	result, err := euria.ReadStream(stream)
	if err != nil {
		return err
	}
	return writeChatResult(stdout, opts.Output, result)
}

func readHistory(path string) ([]euria.HistoryMessage, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read guest history file: %w", err)
	}
	var messages []euria.HistoryMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("decode guest history JSON: %w", err)
	}
	for index, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			return nil, fmt.Errorf("guest history message %d must have role user or assistant", index+1)
		}
	}
	return messages, nil
}

func discoverSessions(ctx context.Context, selector, browser string, dependencies Dependencies) ([]session.Session, error) {
	source := dependencies.CookieSource
	if source == nil {
		source = session.NewChromiumCookieSource()
	}
	source = selectBrowserSource(source, browser)
	return session.Discover(ctx, source, selector)
}

type browserCookieSource struct {
	source  session.CookieSource
	browser string
}

func (s browserCookieSource) Profiles(ctx context.Context, selector string) ([]session.Profile, error) {
	profiles, err := s.source.Profiles(ctx, selector)
	if err != nil || s.browser == "" {
		return profiles, err
	}
	filtered := make([]session.Profile, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Browser == s.browser {
			filtered = append(filtered, profile)
		}
	}
	return filtered, nil
}

func (s browserCookieSource) Cookies(ctx context.Context, profile session.Profile, target *url.URL) ([]*http.Cookie, error) {
	return s.source.Cookies(ctx, profile, target)
}

func selectBrowserSource(source session.CookieSource, browser string) session.CookieSource {
	if browser == "" {
		return source
	}
	return browserCookieSource{source: source, browser: browser}
}

func selectSession(sessions []session.Session) (session.Session, error) {
	if len(sessions) == 0 {
		return session.Session{}, noSignedInSessionError()
	}
	if len(sessions) > 1 {
		return session.Session{}, requireProfileSelection("", sessionProfiles(sessions))
	}
	return sessions[0], nil
}

func noSignedInSessionError() error {
	return errors.New("no signed-in Euria session found\n\n" +
		"  " + signInInstruction + "\n" +
		"  Check your session with: euria auth status\n" +
		"  Or use guest mode: euria --guest --chat \"YOUR MESSAGE\"")
}

func writeSessionStatus(writer io.Writer, signedIn bool) error {
	status := "Euria session: signed in\n"
	if !signedIn {
		status = "Euria session: not signed in\n" + signInInstruction + "\n"
	}
	_, err := io.WriteString(writer, status)
	return err
}

func signedInSessions(ctx context.Context, client *euria.Client, candidates []session.Session) ([]session.Session, error) {
	var signedIn []session.Session
	for _, candidate := range candidates {
		ok, err := client.AuthStatus(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if ok {
			signedIn = append(signedIn, candidate)
		}
	}
	return signedIn, nil
}

func newEuriaClient(dependencies Dependencies) (*euria.Client, error) {
	origin := dependencies.EuriaOrigin
	if origin == "" {
		origin = defaultEuriaOrigin
	}
	return euria.NewClient(origin, dependencies.HTTPClient)
}

func toolsForSearch(mode string) []string {
	switch mode {
	case "on":
		return []string{"web_search_forced"}
	case "off":
		return []string{}
	default:
		return []string{"web_search"}
	}
}

func writeChatResult(writer io.Writer, output string, result euria.ChatResult) error {
	if output == "json" {
		return json.NewEncoder(writer).Encode(result)
	}
	_, err := fmt.Fprintln(writer, result.Answer)
	return err
}

func redactSessionError(err error, userSession session.Session) error {
	message := err.Error()
	for _, cookie := range userSession.Cookies {
		if cookie != nil && cookie.Value != "" {
			message = strings.ReplaceAll(message, cookie.Value, "[redacted]")
		}
	}
	if userSession.XsrfToken != "" {
		message = strings.ReplaceAll(message, userSession.XsrfToken, "[redacted]")
	}
	return errors.New(message)
}
