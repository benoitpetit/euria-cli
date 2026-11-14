package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/benoitpetit/euria_retro/internal/session"
)

func TestParseChatFlags(t *testing.T) {
	got, err := Parse([]string{
		"--chat", "Explain DNS.",
		"--model", "expert",
		"--web-search", "on",
		"--output", "json",
		"--chrome-profile", "/home/test/.config/chromium/Default",
		"--browser", "chromium",
		"--account", "account-123",
		"--conversation", "conversation-456",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Options{
		Command:        "chat",
		Prompt:         "Explain DNS.",
		Model:          "expert",
		WebSearch:      "on",
		Output:         "json",
		ChromeProfile:  "/home/test/.config/chromium/Default",
		Browsers:       []session.Browser{"chromium"},
		AccountID:      "account-123",
		ConversationID: "conversation-456",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseAcceptsRepeatedBrowsersInOrder(t *testing.T) {
	got, err := Parse([]string{"--browser", "firefox", "--browser", "chrome", "--chat", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []session.Browser{"firefox", "chrome"}
	if !reflect.DeepEqual(got.Browsers, want) {
		t.Fatalf("Parse().Browsers = %v, want %v", got.Browsers, want)
	}
}

func TestParsePhase0Flags(t *testing.T) {
	got, err := Parse([]string{
		"--chat", "hello",
		"--thinking",
		"--project", "proj-1",
		"--tool", "wiki_search",
		"--tool", "create_pdf",
		"--login-timeout", "2m",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !got.Thinking {
		t.Fatal("Thinking not set")
	}
	if got.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q", got.ProjectID)
	}
	if !reflect.DeepEqual(got.Tools, []string{"wiki_search", "create_pdf"}) {
		t.Fatalf("Tools = %v", got.Tools)
	}
	if got.LoginTimeout.String() != "2m0s" {
		t.Fatalf("LoginTimeout = %v", got.LoginTimeout)
	}
}

func TestParseRejectsUnsupportedTool(t *testing.T) {
	if _, err := Parse([]string{"--chat", "hello", "--tool", "nope"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsUnsupportedBrowser(t *testing.T) {
	for _, name := range []string{"unknown", "inline"} {
		if _, err := parseBrowsers([]string{name}); err == nil {
			t.Errorf("parseBrowsers(%q) succeeded, want unsupported browser error", name)
		}
	}
}

func TestParseAcceptsSupportedBrowserNames(t *testing.T) {
	want := []string{
		"chrome", "chromium", "edge", "brave", "vivaldi", "opera", "arc", "helium", "dia",
		"comet", "atlas", "whale", "firefox", "zen", "floorp", "waterfox", "librewolf", "safari",
	}
	got, err := parseBrowsers(want)
	if err != nil {
		t.Fatalf("parseBrowsers() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("parseBrowsers() returned %d names, want %d", len(got), len(want))
	}
	for index, browser := range got {
		if string(browser) != want[index] {
			t.Errorf("browser[%d] = %q, want %q", index, browser, want[index])
		}
	}
}

func TestParseProfileRequiresOneBrowser(t *testing.T) {
	for _, args := range [][]string{
		{"--profile", "Default", "--chat", "hello"},
		{"--profile", "Default", "--browser", "chrome", "--browser", "firefox", "--chat", "hello"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) succeeded, want one-browser profile validation", args)
		}
	}
	got, err := Parse([]string{"--profile", "Default", "--browser", "firefox", "--chat", "hello"})
	if err != nil {
		t.Fatalf("Parse() with one selected browser error = %v", err)
	}
	options, err := discoveryOptions(got)
	if err != nil {
		t.Fatalf("discoveryOptions() error = %v", err)
	}
	if options.Profiles[session.Browser("firefox")] != "Default" {
		t.Fatalf("discoveryOptions().Profiles = %v, want Firefox Default", options.Profiles)
	}
}

func TestParseChromeProfileAlias(t *testing.T) {
	got, err := Parse([]string{"--browser", "chrome", "--chrome-profile", "Default", "--chat", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	options, err := discoveryOptions(got)
	if err != nil {
		t.Fatalf("discoveryOptions() error = %v", err)
	}
	if options.Profiles[session.Browser("chrome")] != "Default" {
		t.Fatalf("--chrome-profile alias produced profiles %v, want Chrome Default", options.Profiles)
	}
}

func TestParseRejectsConflictingProfileFlags(t *testing.T) {
	_, err := Parse([]string{
		"--browser", "chrome", "--profile", "Default", "--chrome-profile", "Profile 2", "--chat", "hello",
	})
	if err == nil {
		t.Fatal("Parse() accepted conflicting --profile and --chrome-profile values")
	}
}

func TestParseGuestHistory(t *testing.T) {
	got, err := Parse([]string{"--guest", "--history", "history.json", "--chat", "hello"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !got.Guest || got.HistoryPath != "history.json" {
		t.Fatalf("Parse() = %#v, want guest history.json", got)
	}
}

func TestParseAuthStatus(t *testing.T) {
	got, err := Parse([]string{"auth", "status"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Command != "auth-status" {
		t.Fatalf("Parse().Command = %q, want %q", got.Command, "auth-status")
	}
}

func TestParseAuthLogin(t *testing.T) {
	got, err := Parse([]string{"auth", "login", "--chrome-profile", "Profile 2", "--browser", "chrome"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Command != "auth-login" || got.ChromeProfile != "Profile 2" || !reflect.DeepEqual(got.Browsers, []session.Browser{"chrome"}) {
		t.Fatalf("Parse() = %#v, want auth-login using Profile 2", got)
	}
}

func TestParseAuthLoginRejectsChatOptions(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "login", "--guest"},
		{"auth", "login", "--model", "fast"},
		{"auth", "login", "--chat", "hello"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) accepted an unsupported auth login option", args)
		}
	}
}

func TestUsageDocumentsCommandsFlagsAndExamples(t *testing.T) {
	usage := Usage()
	for _, expected := range []string{
		"euria auth login", "euria auth status", "--chat", "--guest", "--model", "--browser", "--profile",
		"--web-search", "--history", "--output", "--chrome-profile", "--account",
		"--conversation", "--list-models", "--help", "-h",
		"--history is available only with --guest", "cat prompt.txt | euria",
		"--output json", "--conversation CONVERSATION_ID",
		"--browser chrome --browser firefox", "librewolf, safari",
	} {
		if !strings.Contains(usage, expected) {
			t.Errorf("Usage() is missing %q", expected)
		}
	}
}

func TestParseRejectsInvalidBrowserAndGuestBrowserSelection(t *testing.T) {
	for _, args := range [][]string{
		{"--browser", "unknown", "--chat", "hello"},
		{"auth", "login", "--browser", "inline"},
		{"--guest", "--browser", "chrome", "--chat", "hello"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) succeeded, want browser selection validation", args)
		}
	}
}

func TestRejectsHistoryWithoutGuest(t *testing.T) {
	_, err := Parse([]string{"--history", "history.json", "--chat", "hello"})
	if err == nil {
		t.Fatal("Parse() accepted --history without --guest")
	}
}

func TestRejectsInvalidModelAndOutput(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "unknown", "--chat", "hello"},
		{"--output", "xml", "--chat", "hello"},
	} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("Parse(%q) succeeded, want validation error", args)
		}
	}
}

func TestReadPromptRejectsTwoSources(t *testing.T) {
	_, err := ReadPrompt(Options{Prompt: "argument prompt"}, bytes.NewBufferString("stdin prompt"))
	if err == nil {
		t.Fatal("ReadPrompt() accepted both --chat and stdin input")
	}
}

func TestReadPromptFromStdin(t *testing.T) {
	got, err := ReadPrompt(Options{}, bytes.NewBufferString("  prompt from stdin\n"))
	if err != nil {
		t.Fatalf("ReadPrompt() error = %v", err)
	}
	if got != "prompt from stdin" {
		t.Fatalf("ReadPrompt() = %q, want %q", got, "prompt from stdin")
	}
}

func TestReadPromptRejectsEmptyPrompt(t *testing.T) {
	_, err := ReadPrompt(Options{Prompt: " \n\t "}, bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("ReadPrompt() accepted an empty prompt")
	}
}
