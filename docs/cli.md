# Euria Retro — CLI reference

## Commands

- `euria` — print help (or read a prompt from stdin).
- `euria --chat MSG [OPTIONS]` — send one prompt.
- `euria [OPTIONS] < prompt.txt` — read prompt from stdin.
- `euria auth login [...]` — open sign-in and wait for a session.
- `euria auth status [...]` — check for a valid session.
- `euria conversations [list|search QUERY]` — list/search conversations.
- `euria conversation show|rename|archive|unarchive|favorite|unfavorite|share ID` — manage a conversation.
- `euria --list-models` — print supported models.
- `euria completion bash|zsh|fish` — print a shell completion script.

## Global options

- `--browser NAME` (repeatable) — select browser sources.
- `--profile NAME|PATH` — select one profile for one browser.
- `--chrome-profile NAME|PATH` — alias for `--profile`.
- `--account ID` — select an Euria account.
- `--login-timeout DUR` — max wait for sign-in (default 5m).

## Chat options

- `--guest` — anonymous endpoint.
- `--model fast|expert|apertus` (default fast).
- `--web-search auto|on|off` (default auto).
- `--thinking` — enable thinking mode.
- `--project ID` — associate conversation with a project (auth).
- `--tool NAME` (repeatable) — `web_search`, `web_search_forced`, `faq_search`, `wiki_search`, `create_pdf`.
- `--conversation ID` — continue a conversation.
- `--ephemeral` — ephemeral chat without saving (auth).
- `--stream` — stream deltas (text).
- `--output text|json` (default text).
- `--history FILE` — guest history JSON.
- `--audio FILE` — transcribe audio and use as prompt.
- `--cdp URL` — read cookies from a Chrome DevTools Protocol endpoint.
- `--voice` — record from microphone and transcribe.
- `--language CODE` — transcription language.

## Conversation management

- `euria conversations` — list.
- `euria conversations --archived` — archived.
- `euria conversations --favorites` — favorites.
- `euria conversations search QUERY` — search.
- `euria conversation show ID` — show.
- `euria conversation rename ID TITLE` — rename.
- `euria conversation archive|unarchive ID`.
- `euria conversation favorite|unfavorite ID`.
- `euria conversation share ID` — print share URL.

## Audio

Euria accepts `video/mp4`, `video/webm`, `audio/webm`, `audio/mp4`. Convert with ffmpeg:

```sh
ffmpeg -i input.wav -c:a libopus output.webm
```

`--voice` requires one of `sox` (rec), `arecord`, or `ffmpeg` installed. Press Enter to stop recording.