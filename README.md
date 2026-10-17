<p align="center">
  <img src="assets/euria-retro-cover.png" width="1000" alt="Euria Retro: a terminal prompt flows through the redesigned blue gradient Euria logo into a chat response">
</p>

<h1 align="center">Euria Retro</h1>

<p align="center">
  A Go command-line client for Euria, with Chrome/Chromium session reuse and an explicit guest mode.
</p>

---

## Requirements

- Linux and Go 1.25 or newer.
- Chrome or Chromium with an active Euria sign-in for authenticated chat.
- A desktop keyring supported by the browser, such as GNOME Secret Service or KDE Wallet, to decrypt browser cookies.
- `make` is optional; it provides build, install, check, and test targets.
- `jq` is optional for extracting fields from JSON output.

## Install

```sh
git clone https://github.com/benoitpetit/euria_retro.git
cd euria_retro
make build
make install
```

By default, `make install` places the executable at `~/.local/bin/euria`. Set `BINDIR` to choose another install directory. You can also use `bin/euria` directly after `make build`.

## Sign in

Run `euria auth login` to reuse a valid session or open Euria's Infomaniak sign-in flow in your Chrome/Chromium profile. Complete the normal browser sign-in; the CLI detects the session after Euria accepts it. The CLI never asks for or enters your password. Press `Ctrl+C` to stop waiting without closing the browser. A visible Euria chat page can still be in guest mode until the account sign-in finishes.

Euria Retro reads Euria-scoped cookies from the selected local profile using a read-only database connection. Cookie values stay in memory; the CLI does not copy the browser database to a temporary file or save session data. A desktop keyring prompt may appear while encrypted cookies are decrypted.

```sh
euria auth login
euria auth login --chrome-profile "Profile 2"
euria auth login --chrome-profile /path/to/Default --browser chrome
```

Check the detected session:

```sh
euria auth status
```

The command reports whether a session was found in Chrome or Chromium. If it reports `not signed in`, run `euria auth login` to open a profile and sign in. For a one-off prompt without a signed-in account, use guest mode; it is subject to Euria's guest quota:

```sh
euria --guest --chat "Hello!"
```

If several profiles are signed in, select one explicitly. Use its full directory path if the same profile name exists in both Chrome and Chromium:

```sh
euria auth status --chrome-profile "Profile 2"
euria --chat "Explain DNS in one paragraph." --chrome-profile "Profile 2"
```

For a custom profile directory outside Chrome's or Chromium's usual locations, specify its browser explicitly:

```sh
euria auth login --chrome-profile /path/to/Default --browser chrome
euria auth status --chrome-profile /path/to/Default --browser chromium
euria --chat "Explain DNS." --chrome-profile /path/to/Default --browser chrome
```

You can also pass a profile directory path. If the browser profile contains more than one Euria account, provide the account ID you want to use:

```sh
euria --chat "Give me a short summary." --account 12345
```

## Quick start

Running `euria` with no arguments shows the help. A non-empty prompt piped to stdin starts a chat:

```sh
euria
cat prompt.txt | euria --output json
```

The help and README use the same command examples:

```sh
euria auth login
euria auth login --chrome-profile "Profile 2"
euria auth login --chrome-profile /path/to/Default --browser chrome
euria auth status
euria --chat "Explain DNS in one paragraph."
euria --chat "Explain DNS." --output json
euria --guest --chat "What causes the northern lights?"
cat prompt.txt | euria --output json
euria --chat "Continue this discussion." --account 12345 --conversation CONVERSATION_ID
```

Authenticated chat is the default:

```sh
euria --chat "Explain how DNS works in one paragraph."
euria --chat "Give me three vegetarian lunch ideas." --model fast
euria --chat "Find recent documentation about Go contexts." --web-search on --output json
```

Text is printed by default. JSON output includes the answer and the authenticated conversation ID:

```sh
euria --chat "Reply with the word OK." --output json
```

```json
{"answer":"OK","conversation_id":"<conversation-id>"}
```

Continue a saved authenticated conversation with its ID:

```sh
euria --chat "Can you give an example?" --conversation <conversation-id>
```

To send a prompt from a file or another command, omit `--chat` and pipe the prompt to stdin:

```sh
cat prompt.txt | euria --output json
```

## Guest chat

Use `--guest` to send the request to Euria's public ephemeral endpoint. Guest mode never inspects or sends browser cookies:

```sh
euria --guest --chat "What causes the northern lights?"
euria --guest --chat "Explain DNS in one paragraph." --output json | jq -r '.answer'
```

The guest model options are `fast`, `expert`, and `apertus`. The public Euria client currently maps `expert` to `fast`. Web search modes are `auto` (the `web_search` tool), `on` (`web_search_forced`), and `off` (no web-search tool).

You can provide prior user and assistant messages as a JSON array. Euria Retro sends this history with the next guest prompt and does not save it locally:

```json
[
  {"role":"user","content":"What is photosynthesis?"},
  {"role":"assistant","content":"Plants use light to turn water and carbon dioxide into sugars and oxygen."}
]
```

```sh
euria --guest --chat "How does chlorophyll help?" --history history.json
```

`--history` is accepted only with `--guest`; authenticated conversations use Euria's server-side history instead.

## Options

| Option | Values | Default | Description |
| --- | --- | --- | --- |
| `auth login` | optional `--chrome-profile NAME or PATH`, `--browser chrome or chromium` | — | Reuse a session or open Infomaniak sign-in in Chrome/Chromium and wait for Euria to accept it. |
| `auth status` | optional `--chrome-profile NAME or PATH`, `--browser chrome or chromium` | — | Check whether a selected Chrome/Chromium profile has a valid Euria session. |
| `--chat` | `MSG` | — | Send one prompt. Use stdin when omitted. |
| `--guest` | — | off | Use the anonymous public endpoint without browser credentials. |
| `--model` | `fast`, `expert`, `apertus` | `fast` | Select an Euria model. |
| `--web-search` | `auto`, `on`, `off` | `auto` | Select automatic, forced, or disabled web search. |
| `--history` | `FILE` | — | Load prior messages in guest mode only. |
| `--output` | `text`, `json` | `text` | Print the answer or a JSON object. |
| `--chrome-profile` | `NAME` or path | automatic | Select a Chrome/Chromium profile; use a full path if names collide. |
| `--browser` | `chrome` or `chromium` | automatic | Select the browser for a custom profile path that cannot be identified from its location. |
| `--account` | `ID` | automatic | Select an account when the session has several. |
| `--conversation` | `ID` | new conversation | Continue a saved authenticated conversation. |
| `--list-models` | — | — | Print model names currently supported by this CLI. |
| `-h`, `--help` | — | — | Show usage. |

`--history` requires `--guest`. `--account` and `--conversation` use authenticated mode. With no arguments and empty stdin, the CLI prints help.

## Privacy and service behavior

- Prompts and authenticated conversation contents are sent to Infomaniak. Authenticated conversations are saved in the signed-in Euria account; this CLI does not delete them.
- Browser cookie values and the XSRF token stay in process memory and are sent only to the Euria HTTPS origin. The CLI does not write them to a config file, history file, logs, or JSON output.
- Guest mode sends no browser cookies or XSRF token. Euria currently reports a guest limit of five messages per day, but the service controls the actual quota and may change it.
- Chat POST requests are never retried automatically. A connection can fail after Euria has accepted a prompt, and a retry could create a duplicate conversation.
- The account and guest routes are private web-client endpoints observed during reverse engineering, not a documented public API contract. Euria can change them at any time.

## Development

```sh
make check
make test
make build
bin/euria --help
bin/euria --list-models
```

`make check` checks Go formatting and runs `go vet`. `make test` runs the local Go test suite and does not contact Euria. A live prompt sends a request to the selected account or guest endpoint.
