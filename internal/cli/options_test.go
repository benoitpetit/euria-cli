package cli

import (
	"bytes"
	"strings"
	"testing"
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
		Browser:        "chromium",
		AccountID:      "account-123",
		ConversationID: "conversation-456",
	}
	if got != want {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
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
	if got.Command != "auth-login" || got.ChromeProfile != "Profile 2" || got.Browser != "chrome" {
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
		"euria auth login", "euria auth status", "--chat", "--guest", "--model", "--browser",
		"--web-search", "--history", "--output", "--chrome-profile", "--account",
		"--conversation", "--list-models", "--help", "-h",
		"--history is available only with --guest", "cat prompt.txt | euria",
		"--output json", "--conversation CONVERSATION_ID",
	} {
		if !strings.Contains(usage, expected) {
			t.Errorf("Usage() is missing %q", expected)
		}
	}
}

func TestParseRejectsInvalidBrowserAndGuestBrowserSelection(t *testing.T) {
	for _, args := range [][]string{
		{"--browser", "firefox", "--chat", "hello"},
		{"auth", "login", "--browser", "firefox"},
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
