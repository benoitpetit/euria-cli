# Euria CLI — how it talks to Euria

This document describes the request flow between the CLI and the Euria web service. Euria has no public API; the CLI uses the same private web-client endpoints the browser uses.

## Overview

- Base origin: `https://euria.infomaniak.com` (HTTPS only, standard port).
- Authenticated routes live under `/api/1/accounts/{accountID}/...`.
- Guest route: `POST /api/1/public/conversations/ephemeral`.
- Responses are Server-Sent Events (SSE) for chat, JSON otherwise.

## Authentication

The CLI never stores credentials. It reuses a signed-in browser session:

1. Discover Euria cookies from local browser profiles (Sweetcookie, or a CDP endpoint).
2. Group cookies by browser/profile/store.
3. Validate each candidate with `GET /api/1/users/me` (2xx = signed in).
4. List accounts with `GET /api/1/users/me/accounts` and select one.

Cookies and the `EURIA-API-XSRF-TOKEN` value stay in process memory. Chromium/Firefox databases are read from temporary snapshots; nothing is persisted by the CLI.

## Cookie handling

- Only cookies for `euria.infomaniak.com` / `infomaniak.com` are kept.
- Expired cookies and non-root paths are dropped.
- Firefox account-container cookies are skipped.
- The XSRF token is sent as `X-XSRF-TOKEN`, taken from the jar or the discovered value.

## Chat flow (authenticated)

1. `POST /api/1/accounts/{accountID}/conversations` for a new conversation, or
   `POST /api/1/accounts/{accountID}/conversations/{conversationID}/messages` to continue (with `parent_id` = last message ID).
2. Payload: `{ content, file_ids, tools, model, thinking, project_id }`.
3. The server streams SSE. The CLI reads until the completion marker (`</stream>` or `[DONE]`), joining `choices[].delta.content`.

## Guest flow

`POST /api/1/public/conversations/ephemeral` with `{ messages, model, tools }`. No cookies or XSRF token. The public endpoint maps `expert` to `fast` and enforces a daily quota (HTTP 402 when exceeded).

## Transcription

`POST /api/1/public/audios/transcript` or `/api/1/accounts/{accountID}/audios/transcript` with a multipart `file` field. Accepted types: `video/mp4`, `video/webm`, `audio/webm`, `audio/mp4`. Response: `{ "result": "success", "data": "..." }`.

## Error handling

- 401/419 → session expired.
- 402 (guest) → quota reached.
- 429 → rate limited.
- Other statuses → `Euria returned HTTP N` (JSON message extracted when present).
- Network errors are normalized (timeout, DNS, TLS).
- Session values are redacted from error messages.

## Notes

These routes are private web-client endpoints observed during reverse engineering. Euria can change them at any time. Chat POSTs are never retried automatically.