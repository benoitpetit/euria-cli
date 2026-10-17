package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benoitpetit/euria_retro/internal/session"
)

// Options contains the command and flags selected by the user.
type Options struct {
	Command        string
	Prompt         string
	Guest          bool
	Model          string
	WebSearch      string
	HistoryPath    string
	Output         string
	ChromeProfile  string
	Profile        string
	Browsers       []session.Browser
	AccountID      string
	ConversationID string
}

// Parse validates the CLI flags and identifies the selected command.
func Parse(args []string) (Options, error) {
	opts := Options{
		Command:   "chat",
		Model:     "fast",
		WebSearch: "auto",
		Output:    "text",
	}
	flagArgs := args
	authCommand := ""
	if len(args) > 0 && args[0] == "auth" {
		if len(args) < 2 || (args[1] != "login" && args[1] != "status") {
			return Options{}, fmt.Errorf("unknown command %q; expected 'auth login' or 'auth status'", strings.Join(args, " "))
		}
		authCommand = args[1]
		opts.Command = "auth-" + authCommand
		flagArgs = args[2:]
	}

	fs := flag.NewFlagSet("euria", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var listModels, help bool
	var browserValues browserFlags
	fs.StringVar(&opts.ChromeProfile, "chrome-profile", "", "Select a Chrome/Chromium profile by name or path")
	fs.StringVar(&opts.Profile, "profile", "", "Select one browser profile by name or path")
	fs.Var(&browserValues, "browser", "Select a browser source; may be repeated")
	fs.BoolVar(&help, "help", false, "Show this help")
	fs.BoolVar(&help, "h", false, "Show this help")
	if authCommand == "" {
		fs.StringVar(&opts.Prompt, "chat", "", "Send a user message")
		fs.BoolVar(&opts.Guest, "guest", false, "Use the anonymous guest endpoint")
		fs.StringVar(&opts.Model, "model", opts.Model, "Select fast, expert, or apertus")
		fs.StringVar(&opts.WebSearch, "web-search", opts.WebSearch, "Select auto, on, or off")
		fs.StringVar(&opts.HistoryPath, "history", "", "Load guest conversation history from a JSON file")
		fs.StringVar(&opts.Output, "output", opts.Output, "Output as text or json")
		fs.StringVar(&opts.AccountID, "account", "", "Select an Euria account by ID")
		fs.StringVar(&opts.ConversationID, "conversation", "", "Continue an authenticated conversation by ID")
		fs.BoolVar(&listModels, "list-models", false, "List model names observed by this CLI")
	}
	if err := fs.Parse(flagArgs); err != nil {
		return Options{}, err
	}
	browsers, err := parseBrowsers(browserValues)
	if err != nil {
		return Options{}, err
	}
	opts.Browsers = browsers
	if help {
		opts.Command = "help"
		return opts, nil
	}
	if len(fs.Args()) != 0 {
		return Options{}, fmt.Errorf("unexpected positional argument %q; use --chat MSG or provide the prompt on stdin", fs.Args()[0])
	}
	if opts.Profile != "" && opts.ChromeProfile != "" && opts.Profile != opts.ChromeProfile {
		return Options{}, fmt.Errorf("--profile and --chrome-profile select different profiles; use only one")
	}
	if authCommand != "" {
		if _, err := discoveryOptions(opts); err != nil {
			return Options{}, err
		}
		return opts, nil
	}
	if listModels {
		opts.Command = "list-models"
		if opts.Prompt != "" || opts.Guest || opts.HistoryPath != "" || opts.AccountID != "" || opts.ConversationID != "" || opts.Profile != "" || opts.ChromeProfile != "" || len(opts.Browsers) != 0 {
			return Options{}, fmt.Errorf("--list-models cannot be combined with chat options")
		}
		return opts, nil
	}

	if opts.Model != "fast" && opts.Model != "expert" && opts.Model != "apertus" {
		return Options{}, fmt.Errorf("unsupported model %q; choose fast, expert, or apertus", opts.Model)
	}
	if opts.WebSearch != "auto" && opts.WebSearch != "on" && opts.WebSearch != "off" {
		return Options{}, fmt.Errorf("invalid --web-search value %q; choose auto, on, or off", opts.WebSearch)
	}
	if opts.Output != "text" && opts.Output != "json" {
		return Options{}, fmt.Errorf("invalid --output value %q; choose text or json", opts.Output)
	}
	if opts.HistoryPath != "" && !opts.Guest {
		return Options{}, fmt.Errorf("--history is available only with --guest")
	}
	if opts.Guest && (opts.ChromeProfile != "" || opts.Profile != "" || len(opts.Browsers) != 0 || opts.AccountID != "" || opts.ConversationID != "") {
		return Options{}, fmt.Errorf("--profile, --chrome-profile, --browser, --account, and --conversation require authenticated mode")
	}
	if _, err := discoveryOptions(opts); err != nil {
		return Options{}, err
	}
	return opts, nil
}

type browserFlags []string

func (f *browserFlags) String() string { return strings.Join(*f, ",") }

func (f *browserFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
}

var supportedBrowsers = map[string]struct{}{
	"chrome": {}, "chromium": {}, "edge": {}, "brave": {}, "vivaldi": {}, "opera": {},
	"arc": {}, "helium": {}, "dia": {}, "comet": {}, "atlas": {}, "whale": {},
	"firefox": {}, "zen": {}, "floorp": {}, "waterfox": {}, "librewolf": {}, "safari": {},
}

func parseBrowsers(values []string) ([]session.Browser, error) {
	browsers := make([]session.Browser, 0, len(values))
	for _, value := range values {
		name := strings.ToLower(strings.TrimSpace(value))
		if _, ok := supportedBrowsers[name]; !ok {
			return nil, fmt.Errorf("unsupported --browser value %q; see --help for supported browser names", value)
		}
		browsers = append(browsers, session.Browser(name))
	}
	return browsers, nil
}

func discoveryOptions(opts Options) (session.DiscoveryOptions, error) {
	browsers := opts.Browsers
	profile := opts.Profile
	if opts.ChromeProfile != "" {
		if profile != "" && profile != opts.ChromeProfile {
			return session.DiscoveryOptions{}, fmt.Errorf("--profile and --chrome-profile select different profiles; use only one")
		}
		profile = opts.ChromeProfile
	}
	result := session.DiscoveryOptions{Browsers: append([]session.Browser(nil), browsers...)}
	if profile == "" {
		return result, nil
	}
	if len(browsers) != 1 {
		return session.DiscoveryOptions{}, fmt.Errorf("a profile must be paired with exactly one --browser; for example: --browser chrome --profile Default")
	}
	result.Profiles = map[session.Browser]string{browsers[0]: profile}
	return result, nil
}

// ReadPrompt returns the argument prompt or reads one prompt from stdin.
func ReadPrompt(opts Options, stdin io.Reader) (string, error) {
	prompt := strings.TrimSpace(opts.Prompt)
	if stdin != nil && !isTerminal(stdin) {
		input, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		stdinPrompt := strings.TrimSpace(string(input))
		if prompt != "" && stdinPrompt != "" {
			return "", fmt.Errorf("provide the prompt with --chat or stdin, not both")
		}
		if prompt == "" {
			prompt = stdinPrompt
		}
	}
	if prompt == "" {
		return "", fmt.Errorf("a prompt is required: use --chat MSG or provide it on stdin")
	}
	return prompt, nil
}

func isTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Usage returns the help text shown by --help.
func Usage() string {
	return `Usage:
  euria
  euria --chat MSG [OPTIONS]
  euria [OPTIONS] < prompt.txt
  euria auth login [--browser NAME ...] [--profile NAME|PATH]
  euria auth status [--browser NAME ...] [--profile NAME|PATH]

Commands:
  auth login           Open the Infomaniak sign-in flow in your default browser
  auth status          Check for a valid Euria session in selected browser sources
  --list-models        Print the model names supported by this CLI
  chat                 Send one prompt with --chat MSG or from stdin

Options:
  --chat MSG             Send one user message; otherwise read one prompt from stdin
  --guest                Use the anonymous endpoint (default: signed-in account)
  --model MODEL          Model: fast, expert, or apertus (default: fast)
  --web-search MODE      Search: auto, on, or off (default: auto)
  --history FILE         JSON array of prior messages; guest mode only
  --output FORMAT        Output: text or json (default: text)
  --browser NAME         Select a browser source; repeat to check several
  --profile NAME|PATH    Select one profile for the selected browser
  --chrome-profile NAME|PATH
                         Alias for --profile (kept for compatibility)
  --account ID           Select an Euria account when the session has multiple accounts
  --conversation ID      Continue an existing authenticated conversation
  --list-models          Print supported model names
  -h, --help             Show this help

Examples:
  euria auth login
  euria auth status
  euria auth status --browser chrome --profile "Profile 2"
  euria auth status --browser chrome --browser firefox
  euria auth login --browser chrome --chrome-profile /path/to/Default
  euria --chat "Explain DNS in one paragraph."
  euria --chat "Explain DNS." --output json
  euria --guest --chat "What causes the northern lights?"
  cat prompt.txt | euria --output json
  euria --chat "Continue this discussion." --account 12345 --conversation CONVERSATION_ID

Notes:
  --history is available only with --guest. Authenticated conversations are
  saved to your Euria account. Without --browser, default browser profiles
  are checked. Use --browser more than once to select several sources.
  An explicit --profile or --chrome-profile requires exactly one --browser.
  auth login opens the system browser, which may differ from the selected
  source. Sign in in the selected source or open the Euria URL there.
  Supported names: chrome, chromium, edge, brave, vivaldi, opera, arc, helium,
  dia, comet, atlas, whale, firefox, zen, floorp, waterfox, librewolf, safari.
`
}
