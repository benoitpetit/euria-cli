# Changelog

## Unreleased

- Add `--thinking`, `--project`, `--tool`, `--ephemeral`, `--stream` flags.
- Add `--audio` and `--voice` (microphone recording + transcription).
- Add `--cdp` cookie source via Chrome DevTools Protocol.
- Add conversation management commands (`conversations`, `conversation`).
- Add shell completion (`completion bash|zsh|fish`).
- Normalize HTTP/network errors and reduce noisy warnings.
- Update documentation (README, docs/cli.md, docs/action-plan.md).
- Replace the Bash and PowerShell scripts with a Linux-first Go CLI.
- Reuse a signed-in Chrome/Chromium Euria session and keep guest chat explicit.
- Add account selection, conversation continuation, streaming responses, and JSON output.
- Read Chromium cookie databases directly, keep session values in memory, and avoid replaying chat POSTs through redirects.
- Validate signed-in profiles before choosing among browser profiles and redact server diagnostics.
- Remove the old shell clients and quota simulation.

## 0.1.0 - 2026-10-16

- Add Bash and PowerShell 7 clients for Euria’s public guest chat.
- Support guest models, web-search modes, supplied message history, and text or JSON output.
- Document request handling, quotas, and the limits of server-retention guarantees.
