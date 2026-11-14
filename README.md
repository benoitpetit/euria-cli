<p align="center">
  <img src="assets/euria-retro-cover.png" width="1000" alt="Euria Retro: a friendly terminal prompt connects to a chat response">
</p>

<h1 align="center">Euria Retro</h1>

<p align="center">
  A Go command-line client for Euria, with multi-browser session reuse and guest mode.
</p>

---

## Platforms and browsers

Euria Retro runs on Linux, Windows, and macOS. Prebuilt releases are provided for the targets listed below; Go 1.25 or newer is needed only when building from source.

| Browser families | Linux | Windows | macOS |
| --- | --- | --- | --- |
| Chrome, Chromium, Edge, Brave, Vivaldi, Opera, Whale | Yes | Yes | Yes |
| Firefox, Zen, Floorp, Waterfox, LibreWolf | Yes | Yes | Yes |
| Arc, Comet | — | Yes | Yes |
| Dia, ChatGPT Atlas, Safari | — | — | Yes |
| Helium | — | — | Yes, selected explicitly |

Cookie support is provided by [Sweetcookie](https://github.com/steipete/sweetcookie). Some browser versions or profile layouts may not be readable. On Windows, recent Chromium builds can use app-bound encryption that prevents cookie decryption. A system keychain or keyring prompt may appear when the browser protects cookies with OS credentials.

`auth login` opens the system's default browser. Linux uses `xdg-open`; macOS uses `open`; Windows uses the system URL handler. The system browser may differ from the selected cookie source. When you pass `--browser` or `--profile`, finish signing in in that browser and profile; if the URL opened elsewhere, open `https://euria.infomaniak.com` in the selected browser.

Alternatively, you can read cookies from a browser exposing a Chrome DevTools Protocol endpoint (started with `--remote-debugging-port=9222`):

```sh
chromium --remote-debugging-port=9222 --user-data-dir=/tmp/euria-profile
euria auth status --cdp http://localhost:9222
euria --chat "Hello" --cdp http://localhost:9222
```

## Quick install

Linux / macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/benoitpetit/euria_retro/main/install.sh | sh
```

Install to a custom directory:

```sh
curl -fsSL https://raw.githubusercontent.com/benoitpetit/euria_retro/main/install.sh | sh -s -- --dir /usr/local/bin
```

Install a specific version:

```sh
curl -fsSL https://raw.githubusercontent.com/benoitpetit/euria_retro/main/install.sh | sh -s -- --version v0.0.4
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/benoitpetit/euria_retro/main/install.ps1 | iex
```

Install to a custom directory:

```powershell
.\install.ps1 -Dir "$env:LOCALAPPDATA\euria\bin"
```

Install a specific version:

```powershell
.\install.ps1 -Version v0.0.4
```
The installer detects your OS and architecture, downloads the matching release asset, verifies its SHA-256 checksum, and installs `euria` into `~/.local/bin` (Linux/macOS) or `%LOCALAPPDATA%\euria\bin` (Windows).

## Install

Download the asset for your OS and architecture from [GitHub Releases](https://github.com/benoitpetit/euria_retro/releases). Release asset names:

| Platform | Asset |
| --- | --- |
| Linux x86-64 | `euria_linux_amd64` |
| Linux ARMv7 | `euria_linux_armv7` |
| Linux ARM64 / AArch64 | `euria_linux_arm64` |
| Windows x86-64 | `euria_windows_amd64.exe` |
| Windows ARM64 | `euria_windows_arm64.exe` |
| macOS Intel | `euria_darwin_amd64` |
| macOS Apple silicon | `euria_darwin_arm64` |

For example, download and run the latest Linux x86-64 build:

```sh
curl -fL https://github.com/benoitpetit/euria_retro/releases/latest/download/euria_linux_amd64 -o euria_linux_amd64
curl -fL https://github.com/benoitpetit/euria_retro/releases/latest/download/SHA256SUMS -o SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
chmod +x euria_linux_amd64
./euria_linux_amd64 --help
```

On Windows PowerShell, download the matching `.exe` asset from the release page and run `./euria_windows_amd64.exe --help` (or use the ARM64 asset on Windows ARM devices).

Each release includes `SHA256SUMS`. On Linux, verify the downloaded asset from the same directory with:

```sh
sha256sum -c SHA256SUMS
```

On macOS, verify one downloaded asset with `grep ' euria_darwin_arm64$' SHA256SUMS | shasum -a 256 -c -` (replace the asset name as needed). On Windows PowerShell, compare the asset's `Get-FileHash -Algorithm SHA256` result with its matching hash in `SHA256SUMS`.

To build and install from source on Linux or macOS:

```sh
git clone https://github.com/benoitpetit/euria_retro.git
cd euria_retro
make build
make install
```

`make install` places `euria` in `~/.local/bin` by default. Set `BINDIR` to choose another directory. `make` is optional; `jq` is optional for extracting JSON fields.

The release workflow runs Go tests and `go vet` on native Linux, Windows, and macOS runners, then cross-compiles the seven assets above whenever a `v*` tag is pushed. Adding the workflow does not rebuild earlier tags; the next release is triggered by a new tag.

## Sign in

Run `euria auth login` to check for an existing Euria session or open the Infomaniak sign-in page in your default browser. The CLI never asks for or enters your password. It waits until the selected browser sources contain a session accepted by Euria. Press `Ctrl+C` to stop waiting without closing the browser.

By default, Euria Retro checks one default profile per browser: `Default` for Chromium-family browsers, `default-release` for Firefox-family browsers, and the system cookie store for Safari. Use repeated `--browser` flags to select sources. Use `--profile` with exactly one browser to select another profile by name or path; `--chrome-profile` remains an alias for compatibility.

```sh
euria auth login
euria auth status
euria auth status --browser chrome --profile "Profile 2"
euria auth status --browser chrome --browser firefox
euria --chat "Explain DNS in one paragraph." --browser chrome --profile "Profile 2"
euria auth login --browser chrome --chrome-profile /path/to/Default
```

Run `euria auth status` to check whether Euria accepts the discovered session. If no session is found, `euria auth login` opens the sign-in page. For a one-off prompt without a signed-in account, use guest mode; Euria applies its guest quota:

```sh
euria --guest --chat "Hello!"
```

Sweetcookie reads Chromium and Firefox cookie databases through temporary snapshots, including their write-ahead log sidecars. Cookie values stay in process memory; Euria Retro does not persist session data. Firefox account-container cookies are not used because the CLI cannot safely combine or select container sessions. If the same profile name exists in multiple browsers, select the browser first and then pass its profile name or path.

## Quick start

Running `euria` with no arguments shows the help. A non-empty prompt piped to stdin starts a chat:

```sh
euria
cat prompt.txt | euria --output json
```

The help and README use the same command examples:

```sh
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

## Conversations and projects (authenticated)

List, search, and manage saved conversations:

```sh
euria conversations
euria conversations search "DNS"
euria conversations --archived
euria conversations --favorites
euria conversation show CONVERSATION_ID
euria conversation rename CONVERSATION_ID "New title"
euria conversation archive CONVERSATION_ID
euria conversation unarchive CONVERSATION_ID
euria conversation favorite CONVERSATION_ID
euria conversation unfavorite CONVERSATION_ID
euria conversation share CONVERSATION_ID
```

Ephemeral chat (no saved conversation):

```sh
euria --chat "Quick question" --ephemeral
```

Streaming output:

```sh
euria --chat "Write a long answer." --stream
```

Thinking mode and tools:

```sh
euria --chat "Solve this." --thinking --tool wiki_search --tool create_pdf
```

## Audio and voice

Transcribe an audio file and use it as the prompt (guest or authenticated):

```sh
euria --guest --audio recording.webm --language en
euria --audio recording.webm --account 12345
```

Record directly from the microphone (requires `sox`/`rec`, `arecord`, or `ffmpeg`):

```sh
euria --guest --voice
euria --voice --chat "Summarize this:"
```

Euria accepts `video/mp4`, `video/webm`, `audio/webm`, `audio/mp4` files. Convert with, for example:

```sh
ffmpeg -i input.wav -c:a libopus output.webm
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
| `auth login` | optional `--browser NAME` and `--profile NAME or PATH` | — | Reuse a session or open Infomaniak sign-in in the system browser and wait for Euria to accept it. |
| `auth status` | optional `--browser NAME` and `--profile NAME or PATH` | — | Check whether a browser session is accepted by Euria. |
| `--chat` | `MSG` | — | Send one prompt. Use stdin when omitted. |
| `--guest` | — | off | Use the anonymous public endpoint without browser credentials. |
| `--model` | `fast`, `expert`, `apertus` | `fast` | Select an Euria model. |
| `--web-search` | `auto`, `on`, `off` | `auto` | Select automatic, forced, or disabled web search. |
| `--history` | `FILE` | — | Load prior messages in guest mode only. |
| `--output` | `text`, `json` | `text` | Print the answer or a JSON object. |
| `--browser` | Sweetcookie browser name; repeatable | automatic | Select browser sources to check. Supported names: `chrome`, `chromium`, `edge`, `brave`, `vivaldi`, `opera`, `arc`, `helium`, `dia`, `comet`, `atlas`, `whale`, `firefox`, `zen`, `floorp`, `waterfox`, `librewolf`, `safari`. |
| `--profile` | `NAME` or path | browser default | Select a profile for exactly one selected browser. |
| `--chrome-profile` | `NAME` or path | browser default | Compatibility alias for `--profile`. |
| `--archived` | — | off | List archived conversations. |
| `--favorites` | — | off | List favorite conversations. |
| `--login-timeout` | `DUR` | `5m` | Maximum time to wait for browser sign-in. |
| `--cdp` | `URL` | — | Read cookies from a Chrome DevTools Protocol endpoint (e.g. `http://localhost:9222`). |
| `--account` | `ID` | automatic | Select an account when the session has several. |
| `--conversation` | `ID` | new conversation | Continue a saved authenticated conversation. |
| `--list-models` | — | — | Print model names currently supported by this CLI. |
| `--thinking` | — | off | Enable Euria thinking mode. |
| `--project` | `ID` | — | Associate a new authenticated conversation with a project. |
| `--tool` | `NAME` | — | Add an Euria tool (repeatable): `web_search`, `web_search_forced`, `faq_search`, `wiki_search`, `create_pdf`. |
| `--ephemeral` | — | off | Use ephemeral chat without saving a conversation (authenticated mode). |
| `--stream` | — | off | Stream answer deltas as they arrive (text output). |
| `--audio` | `FILE` | — | Transcribe an audio file and use it as the prompt. |
| `--voice` | — | off | Record from the microphone and transcribe (requires sox/arecord/ffmpeg). |
| `--language` | `CODE` | from `LANG` or `en` | Language for audio transcription. |
| `-h`, `--help` | — | — | Show usage. |

`--history` requires `--guest`. `--account` and `--conversation` use authenticated mode. With no arguments and empty stdin, the CLI prints help.

## Privacy and service behavior

- Prompts and authenticated conversation contents are sent to Infomaniak. Authenticated conversations are saved in the signed-in Euria account; this CLI does not delete them.
- Browser cookie values and the XSRF token stay in process memory and are sent only to the Euria HTTPS origin. Chromium and Firefox databases are read from temporary snapshots; session data is not saved by the CLI.
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
