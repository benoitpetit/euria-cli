package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/benoitpetit/euria-cli/internal/euria"
	"github.com/benoitpetit/euria-cli/internal/session"
)

const defaultEuriaOrigin = "https://euria.infomaniak.com"
const euriaLoginURL = session.EuriaLoginURL

const (
	defaultLoginPollInterval = 2 * time.Second
	defaultLoginTimeout      = 5 * time.Minute
)

const signInInstruction = "Sign in at https://euria.infomaniak.com in your browser."

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
	if opts.LoginTimeout > 0 {
		dependencies.LoginTimeout = opts.LoginTimeout
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
		return runChat(ctx, opts, nil, stdout, stderr, dependencies)
	}
	switch opts.Command {
	case "help":
		_, err := io.WriteString(stdout, Usage())
		return err
	case "list-models":
		_, err := io.WriteString(stdout, "fast\nexpert\napertus\n")
		return err
	case "auth-status":
		return runAuthStatus(ctx, opts, stdout, stderr, dependencies)
	case "auth-login":
		return runAuthLogin(ctx, opts, stdout, stderr, dependencies)
	case "conversations":
		return runConversations(ctx, opts, stdout, stderr, dependencies)
	case "conversation":
		return runConversation(ctx, opts, stdout, stderr, dependencies)
	case "completion":
		return runCompletion(opts, stdout)
	case "version":
		_, err := io.WriteString(stdout, "euria 0.0.6\n")
		return err
	case "update":
		return runUpdate(ctx, stdout, stderr, opts)
	default:
		return runChat(ctx, opts, stdin, stdout, stderr, dependencies)
	}
}

func runAuthLogin(ctx context.Context, opts Options, stdout, stderr io.Writer, dependencies Dependencies) error {
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	result, err := discoverSessions(ctx, opts, dependencies)
	if err != nil {
		return err
	}
	signedIn, err := signedInSessions(ctx, client, result.Sessions)
	if err != nil {
		return err
	}
	if err := reportDiscoveryWarnings(stderr, opts, result.Warnings, len(signedIn) > 0); err != nil {
		return err
	}
	if len(signedIn) > 0 {
		return writeSessionStatus(stdout, true)
	}

	launcher := dependencies.BrowserLauncher
	if launcher == nil {
		launcher = session.NewSystemBrowserLauncher()
	}
	if err := launcher.Open(euriaLoginURL); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stderr, loginInstructions(opts)); err != nil {
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
	if err := waitForLogin(ctx, client, opts, dependencies, result.Warnings, interval, timeout); err != nil {
		return err
	}
	return writeSessionStatus(stdout, true)
}

func waitForLogin(ctx context.Context, client *euria.Client, opts Options, dependencies Dependencies, initialWarnings []string, interval, timeout time.Duration) error {
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastWarnings := warningsForReport(opts, initialWarnings, false)

	for {
		if err := loginCtx.Err(); err != nil {
			return loginWaitError(err, opts, lastWarnings)
		}
		result, err := discoverSessions(loginCtx, opts, dependencies)
		if err != nil {
			if loginCtx.Err() != nil {
				return loginWaitError(loginCtx.Err(), opts, lastWarnings)
			}
			return err
		}
		if latest := warningsForReport(opts, result.Warnings, false); len(latest) > 0 {
			lastWarnings = latest
		}
		if len(result.Sessions) != 0 {
			signedIn, err := signedInSessions(loginCtx, client, result.Sessions)
			if err != nil {
				if loginCtx.Err() != nil {
					return loginWaitError(loginCtx.Err(), opts, lastWarnings)
				}
				return err
			}
			if len(signedIn) != 0 {
				return nil
			}
		}
		select {
		case <-loginCtx.Done():
			return loginWaitError(loginCtx.Err(), opts, lastWarnings)
		case <-ticker.C:
		}
	}
}

func loginWaitError(err error, opts Options, warnings []string) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("login wait cancelled; the browser remains open")
	}
	selected := "a browser profile Euria Retro checks"
	if len(opts.Browsers) > 0 {
		selected = strings.Join(browserNames(opts.Browsers), ", ") + " browser source"
	}
	if opts.Profile != "" {
		selected += " (profile " + opts.Profile + ")"
	}
	message := fmt.Sprintf("timed out waiting for Euria sign-in in %s; finish signing in there, then check with: %s", selected, authStatusCommand(opts))
	if len(warnings) > 0 {
		message += "; latest browser diagnostic: " + strings.Join(warnings, "; ")
	}
	return errors.New(message)
}

func loginInstructions(opts Options) string {
	selected := "a browser profile Euria Retro checks automatically"
	if len(opts.Browsers) > 0 {
		selected = strings.Join(browserNames(opts.Browsers), " or ") + " browser source"
		if opts.Profile != "" {
			selected += " (profile " + opts.Profile + ")"
		}
	}
	return fmt.Sprintf("The system default browser may differ from the source being checked. Sign in using %s; if needed, open https://euria.infomaniak.com there. Press Ctrl+C to cancel.", selected)
}

func browserNames(browsers []session.Browser) []string {
	names := make([]string, len(browsers))
	for index, browser := range browsers {
		names[index] = string(browser)
	}
	return names
}

func authStatusCommand(opts Options) string {
	parts := []string{"euria", "auth", "status"}
	for _, browser := range opts.Browsers {
		parts = append(parts, "--browser", string(browser))
	}
	if opts.Profile != "" {
		parts = append(parts, "--profile", opts.Profile)
	}
	return strings.Join(parts, " ")
}

func runAuthStatus(ctx context.Context, opts Options, stdout, stderr io.Writer, dependencies Dependencies) error {
	result, err := discoverSessions(ctx, opts, dependencies)
	if err != nil {
		return err
	}
	if len(result.Sessions) == 0 {
		if err := reportDiscoveryWarnings(stderr, opts, result.Warnings, false); err != nil {
			return err
		}
		return writeSessionStatus(stdout, false)
	}
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	sessions, err := signedInSessions(ctx, client, result.Sessions)
	if err != nil {
		return err
	}
	if err := reportDiscoveryWarnings(stderr, opts, result.Warnings, len(sessions) > 0); err != nil {
		return err
	}
	return writeSessionStatus(stdout, len(sessions) > 0)
}

func runChat(ctx context.Context, opts Options, stdin io.Reader, stdout, stderr io.Writer, dependencies Dependencies) error {
	if opts.AudioPath == "" && opts.Voice {
		path, err := recordAudio(ctx, stderr)
		if err != nil {
			return err
		}
		opts.AudioPath = path
		defer os.Remove(path)
	}
	if opts.AudioPath != "" && opts.Guest {
		client, err := newEuriaClient(dependencies)
		if err != nil {
			return err
		}
		language := resolveLanguage(opts.Language)
		text, err := client.TranscribeAudioGuest(ctx, language, opts.AudioPath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return errors.New("transcription returned an empty result")
		}
		if opts.Prompt == "" {
			opts.Prompt = text
		} else {
			opts.Prompt = opts.Prompt + "\n" + text
		}
	}
	if opts.AudioPath != "" && !opts.Guest {
		client, err := newEuriaClient(dependencies)
		if err != nil {
			return err
		}
		// discover session first
		discovery, err := discoverSessions(ctx, opts, dependencies)
		if err != nil {
			return err
		}
		if len(discovery.Sessions) == 0 {
			return noSignedInSessionError()
		}
		sessions, err := signedInSessions(ctx, client, discovery.Sessions)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			return noSignedInSessionError()
		}
		userSession, err := selectSession(sessions)
		if err != nil {
			return err
		}
		accounts, err := client.Accounts(ctx, userSession)
		if err != nil {
			return redactSessionError(err, userSession)
		}
		account, err := euria.SelectAccount(accounts, opts.AccountID)
		if err != nil {
			return err
		}
		language := resolveLanguage(opts.Language)
		text, err := client.TranscribeAudio(ctx, userSession, account.ID, language, opts.AudioPath)
		if err != nil {
			return redactSessionError(err, userSession)
		}
		if strings.TrimSpace(text) == "" {
			return errors.New("transcription returned an empty result")
		}
		if opts.Prompt == "" {
			opts.Prompt = text
		} else {
			opts.Prompt = opts.Prompt + "\n" + text
		}
	}
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

	discovery, err := discoverSessions(ctx, opts, dependencies)
	if err != nil {
		return err
	}
	if len(discovery.Sessions) == 0 {
		if err := reportDiscoveryWarnings(stderr, opts, discovery.Warnings, false); err != nil {
			return err
		}
		return noSignedInSessionError()
	}
	sessions, err := signedInSessions(ctx, client, discovery.Sessions)
	if err != nil {
		return err
	}
	if err := reportDiscoveryWarnings(stderr, opts, discovery.Warnings, len(sessions) > 0); err != nil {
		return err
	}
	if len(sessions) == 0 {
		return noSignedInSessionError()
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
	var thinking *bool
	if opts.Thinking {
		value := true
		thinking = &value
	}
	tools := append([]string{}, toolsForSearch(opts.WebSearch)...)
	tools = append(tools, opts.Tools...)
	tools = uniqueStrings(tools)
	var stream io.ReadCloser
	if opts.Ephemeral {
		tools := append([]string{}, toolsForSearch(opts.WebSearch)...)
		tools = append(tools, opts.Tools...)
		tools = uniqueStrings(tools)
		stream, err = client.EphemeralConversation(ctx, userSession, account.ID, euria.GuestRequest{
			Messages: []euria.HistoryMessage{{Role: "user", Content: prompt}},
			Model:    opts.Model,
			Tools:    tools,
			Thinking: thinking,
		})
	} else {
		request := euria.ChatRequest{Message: prompt, Model: opts.Model, Tools: tools, Thinking: thinking}
		if opts.ConversationID == "" {
			stream, err = client.CreateConversation(ctx, userSession, account.ID, opts.ProjectID, request)
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
	}
	if err != nil {
		return err
	}
	defer stream.Close()
	var onDelta func(string)
	if opts.Stream && opts.Output == "text" {
		onDelta = func(chunk string) { _, _ = fmt.Fprint(stdout, chunk) }
	}
	result, err := euria.ReadStreamWithCallback(stream, onDelta)
	if err != nil {
		return redactSessionError(err, userSession)
	}
	if result.ConversationID == "" {
		result.ConversationID = opts.ConversationID
	}
	if opts.Stream && opts.Output == "text" {
		_, err = fmt.Fprintln(stdout)
		return err
	}
	return writeChatResult(stdout, opts.Output, result)
}

func runGuestChat(ctx context.Context, client *euria.Client, opts Options, prompt string, stdout io.Writer) error {
	messages, err := readHistory(opts.HistoryPath)
	if err != nil {
		return err
	}
	messages = append(messages, euria.HistoryMessage{Role: "user", Content: prompt})
	var thinking *bool
	if opts.Thinking {
		value := true
		thinking = &value
	}
	tools := append([]string{}, toolsForSearch(opts.WebSearch)...)
	tools = append(tools, opts.Tools...)
	tools = uniqueStrings(tools)
	stream, err := client.GuestConversation(ctx, euria.GuestRequest{
		Messages: messages,
		Model:    opts.Model,
		Tools:    tools,
		Thinking: thinking,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	var onDelta func(string)
	if opts.Stream && opts.Output == "text" {
		onDelta = func(chunk string) { _, _ = fmt.Fprint(stdout, chunk) }
	}
	result, err := euria.ReadStreamWithCallback(stream, onDelta)
	if err != nil {
		return err
	}
	if opts.Stream && opts.Output == "text" {
		_, err = fmt.Fprintln(stdout)
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

func discoverSessions(ctx context.Context, opts Options, dependencies Dependencies) (session.DiscoveryResult, error) {
	options, err := discoveryOptions(opts)
	if err != nil {
		return session.DiscoveryResult{}, err
	}
	source := dependencies.CookieSource
	if source == nil {
		if opts.CDPEndpoint != "" {
			source = session.NewCDPCookieSource(opts.CDPEndpoint)
		} else {
			source = session.NewSweetcookieCookieSource()
		}
	}
	return source.Discover(ctx, options)
}

func reportDiscoveryWarnings(writer io.Writer, opts Options, warnings []string, usableSession bool) error {
	warnings = warningsForReport(opts, warnings, usableSession)
	if len(warnings) == 0 || writer == nil {
		return nil
	}
	for _, warning := range warnings {
		if strings.TrimSpace(warning) == "" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "euria: warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

// noisyDiagnostic reports whether a discovery warning is an expected,
// non-actionable diagnostic on the current platform (e.g. a browser that is
// not installed, or a keyring that is unavailable).
func noisyDiagnostic(lower string) bool {
	noisy := []string{
		"store not found",
		"profile not found",
		"failed to read linux keyring",
		"via secret-tool",
		"v11 cookies may be unavailable",
		"supported on macos only",
	}
	for _, needle := range noisy {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func warningsForReport(opts Options, warnings []string, usableSession bool) []string {
	if len(opts.Browsers) > 0 {
		return warnings
	}
	if usableSession {
		return nil
	}
	var actionable []string
	for _, warning := range warnings {
		if noisyDiagnostic(strings.ToLower(warning)) {
			continue
		}
		actionable = append(actionable, warning)
	}
	return actionable
}

func selectSession(sessions []session.Session) (session.Session, error) {
	if len(sessions) == 0 {
		return session.Session{}, noSignedInSessionError()
	}
	if len(sessions) > 1 {
		return session.Session{}, errors.New("multiple signed-in Euria sessions were found; select one with --browser NAME and optionally --profile NAME|PATH")
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
			return nil, redactSessionError(err, candidate)
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

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func runConversations(ctx context.Context, opts Options, stdout, stderr io.Writer, dependencies Dependencies) error {
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	userSession, account, err := discoverAccount(ctx, opts, stderr, dependencies, client)
	if err != nil {
		return err
	}
	if opts.Subcommand == "search" {
		query := strings.Join(opts.Positional, " ")
		convs, err := client.SearchConversations(ctx, userSession, account.ID, query)
		if err != nil {
			return redactSessionError(err, userSession)
		}
		return writeConversations(stdout, opts.Output, convs)
	}
	var convs []euria.Conversation
	if opts.Archived {
		convs, err = client.ArchivedConversations(ctx, userSession, account.ID)
	} else if opts.Favorites {
		convs, err = client.FavoriteConversations(ctx, userSession, account.ID)
	} else {
		convs, err = client.Conversations(ctx, userSession, account.ID)
	}
	if err != nil {
		return redactSessionError(err, userSession)
	}
	return writeConversations(stdout, opts.Output, convs)
}

func runConversation(ctx context.Context, opts Options, stdout, stderr io.Writer, dependencies Dependencies) error {
	client, err := newEuriaClient(dependencies)
	if err != nil {
		return err
	}
	userSession, account, err := discoverAccount(ctx, opts, stderr, dependencies, client)
	if err != nil {
		return err
	}
	if len(opts.Positional) == 0 {
		return fmt.Errorf("conversation ID is required")
	}
	id := opts.Positional[0]
	switch opts.Subcommand {
	case "show":
		conv, err := client.GetConversation(ctx, userSession, account.ID, id)
		if err != nil {
			return redactSessionError(err, userSession)
		}
		return writeConversation(stdout, opts.Output, conv)
	case "rename":
		title := strings.Join(opts.Positional[1:], " ")
		if title == "" {
			return fmt.Errorf("a title is required")
		}
		if err := client.RenameConversation(ctx, userSession, account.ID, id, title); err != nil {
			return redactSessionError(err, userSession)
		}
		_, err := fmt.Fprintln(stdout, "renamed")
		return err
	case "archive":
		if err := client.ArchiveConversation(ctx, userSession, account.ID, id); err != nil {
			return redactSessionError(err, userSession)
		}
		_, err := fmt.Fprintln(stdout, "archived")
		return err
	case "unarchive":
		if err := client.UnarchiveConversation(ctx, userSession, account.ID, id); err != nil {
			return redactSessionError(err, userSession)
		}
		_, err := fmt.Fprintln(stdout, "unarchived")
		return err
	case "favorite":
		if err := client.FavoriteConversation(ctx, userSession, account.ID, id); err != nil {
			return redactSessionError(err, userSession)
		}
		_, err := fmt.Fprintln(stdout, "favorited")
		return err
	case "unfavorite":
		if err := client.UnfavoriteConversation(ctx, userSession, account.ID, id); err != nil {
			return redactSessionError(err, userSession)
		}
		_, err := fmt.Fprintln(stdout, "unfavorited")
		return err
	case "share":
		share, err := client.ShareConversation(ctx, userSession, account.ID, id)
		if err != nil {
			return redactSessionError(err, userSession)
		}
		if opts.Output == "json" {
			return json.NewEncoder(stdout).Encode(share)
		}
		_, err = fmt.Fprintf(stdout, "https://euria.infomaniak.com/shared/%s\n", share.ID)
		return err
	default:
		return fmt.Errorf("unknown conversation command %q", opts.Subcommand)
	}
}

func discoverAccount(ctx context.Context, opts Options, stderr io.Writer, dependencies Dependencies, client *euria.Client) (session.Session, euria.Account, error) {
	discovery, err := discoverSessions(ctx, opts, dependencies)
	if err != nil {
		return session.Session{}, euria.Account{}, err
	}
	if len(discovery.Sessions) == 0 {
		if err := reportDiscoveryWarnings(stderr, opts, discovery.Warnings, false); err != nil {
			return session.Session{}, euria.Account{}, err
		}
		return session.Session{}, euria.Account{}, noSignedInSessionError()
	}
	sessions, err := signedInSessions(ctx, client, discovery.Sessions)
	if err != nil {
		return session.Session{}, euria.Account{}, err
	}
	if err := reportDiscoveryWarnings(io.Discard, opts, discovery.Warnings, len(sessions) > 0); err != nil {
		// ignore
	}
	if len(sessions) == 0 {
		return session.Session{}, euria.Account{}, noSignedInSessionError()
	}
	userSession, err := selectSession(sessions)
	if err != nil {
		return session.Session{}, euria.Account{}, err
	}
	accounts, err := client.Accounts(ctx, userSession)
	if err != nil {
		return session.Session{}, euria.Account{}, redactSessionError(err, userSession)
	}
	account, err := euria.SelectAccount(accounts, opts.AccountID)
	if err != nil {
		return session.Session{}, euria.Account{}, err
	}
	return userSession, account, nil
}

func writeConversations(w io.Writer, output string, convs []euria.Conversation) error {
	if output == "json" {
		return json.NewEncoder(w).Encode(convs)
	}
	for _, conv := range convs {
		title := conv.Title
		if title == "" {
			title = "(untitled)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\n", conv.ID, title)
	}
	return nil
}

func writeConversation(w io.Writer, output string, conv euria.Conversation) error {
	if output == "json" {
		return json.NewEncoder(w).Encode(conv)
	}
	title := conv.Title
	if title == "" {
		title = "(untitled)"
	}
	_, err := fmt.Fprintf(w, "%s\t%s\n", conv.ID, title)
	return err
}

func resolveLanguage(value string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if env := os.Getenv("LANG"); len(env) >= 2 {
		code := strings.ToLower(env[:2])
		if code == "fr" || code == "en" || code == "de" || code == "it" || code == "es" {
			return code
		}
	}
	return "en"
}

func recordAudio(ctx context.Context, stderr io.Writer) (string, error) {
	tmpFile, err := os.CreateTemp("", "euria-voice-*.wav")
	if err != nil {
		return "", err
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name())
	wavPath := tmpFile.Name()

	if err := recordWav(ctx, wavPath, stderr); err != nil {
		os.Remove(wavPath)
		return "", err
	}
	if _, err := os.Stat(wavPath); err != nil {
		return "", fmt.Errorf("recording failed: %w", err)
	}

	// Convert to a format accepted by Euria when ffmpeg is available.
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		webmPath := strings.TrimSuffix(wavPath, ".wav") + ".webm"
		cmd := exec.CommandContext(ctx, ffmpegPath, "-y", "-i", wavPath, "-c:a", "libopus", webmPath)
		if err := cmd.Run(); err == nil {
			os.Remove(wavPath)
			return webmPath, nil
		}
	}
	return wavPath, nil
}

func recordWav(ctx context.Context, path string, stderr io.Writer) error {
	_, _ = fmt.Fprintln(stderr, "Recording... press Enter to stop.")

	// Try recorders in order.
	type candidate struct {
		name string
		args []string
	}
	var candidates []candidate
	if _, err := exec.LookPath("rec"); err == nil {
		candidates = append(candidates, candidate{"rec", []string{"-q", path}})
	}
	if _, err := exec.LookPath("arecord"); err == nil {
		candidates = append(candidates, candidate{"arecord", []string{"-q", "-f", "cd", "-t", "wav", path}})
	}
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		args := ffmpegRecordArgs(path)
		candidates = append(candidates, candidate{ffmpegPath, args})
	}
	if len(candidates) == 0 {
		return errors.New("no audio recorder found; install sox (rec), arecord, or ffmpeg")
	}

	var lastErr error
	for _, c := range candidates {
		cmd := exec.CommandContext(ctx, c.name, c.args...)
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		stop := make(chan struct{})
		go func() {
			buf := make([]byte, 1)
			for {
				n, _ := os.Stdin.Read(buf)
				if n > 0 && buf[0] == '\n' {
					break
				}
				if n == 0 {
					break
				}
			}
			if cmd.Process != nil {
				_ = cmd.Process.Signal(os.Interrupt)
			}
			close(stop)
		}()
		_ = cmd.Wait()
		<-stop
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		lastErr = fmt.Errorf("recorder %s did not produce output", c.name)
	}
	if lastErr == nil {
		lastErr = errors.New("recording failed")
	}
	return lastErr
}

func ffmpegRecordArgs(path string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"-y", "-f", "avfoundation", "-i", ":0", path}
	case "windows":
		return []string{"-y", "-f", "dshow", "-i", "audio=Microphone", path}
	default:
		// Try pulse, then alsa as fallback handled by caller trying next? We only return one.
		return []string{"-y", "-f", "pulse", "-i", "default", path}
	}
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
