# New binary: apid — channel-write orchestration across farcd + mediamtx

Status: open

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

A new Go binary, `cmd/apid/`, that becomes the web app's single write
path for channels, fanning each write out to farcd and to mediamtx so
that a channel created through the web app gets both an archive (farcd)
and a live-view (mediamtx) side without the web client having to know
about mediamtx at all, and without extending farcd itself.

## Scope

- `cmd/apid/` — cobra CLI (`Use: "apid"`), same shape as `cmd/farc`/
  `cmd/hls_server`: loads config, runs under
  `signal.NotifyContext(SIGINT, SIGTERM)`.
- `internal/apiconfig` — process configuration, split the same way
  `internal/config`/`internal/hlsconfig` already are: env vars for
  `apid`'s own HTTP address plus the farcd and mediamtx addresses/
  credentials it talks to; a JSON file (with `Save`/`EnsureExists`) for
  anything site-specific that needs to persist across restarts (e.g. any
  channel-id → mediamtx-path-name bookkeeping, if not fully derivable
  on demand).
- `internal/apid` (or similar) — the actual orchestration logic and HTTP
  handlers.

## API surface (called by the web app)

- `POST /channels` — body: channel metadata (name, capture policy,
  prerecord/postrecord, etc.) + the camera's real RTSP URL. Behavior:
  1. Create a mediamtx path via mediamtx's REST config API (e.g. `POST
     /v3/config/paths/add/{channel_id}`) with the camera URL as the
     path's source, and WebRTC serving enabled for it.
  2. Create the farcd channel via farcd's existing `POST /channels`, with
     `rtsp_url` set to mediamtx's own re-served RTSP address for that
     path (e.g. `rtsp://<mediamtx-host>:8554/{channel_id}`) — **not** the
     camera URL that was submitted. farcd ends up connecting to
     mediamtx, not to the camera directly (see spec.md, "Single RTSP
     connection to the camera").
  - Path name = the channel id (stringified) — unique by construction,
    no separate naming scheme needed.
- `PATCH /channels/{id}` — metadata changes pass through to farcd only;
  if the camera RTSP URL changed, update the mediamtx path's source
  (farcd's `rtsp_url`, pointed at mediamtx, does not change).
- `DELETE /channels/{id}` — remove the farcd channel and the mediamtx
  path.
- `GET /channels/live-urls?ids=1,2,3` — **batch** lookup of WHEP URLs for
  a list of channel ids, one request regardless of how many channels are
  checked on the Live page. Do not add a per-channel-id endpoint that
  would make the frontend issue N requests for N checked channels.

## Partial-failure semantics (no rollback, idempotent retry)

If one of the two downstream calls (farcd, mediamtx) fails on create/
update/remove, `apid` does **not** roll back the side that succeeded. It
returns a response indicating which part succeeded/failed, e.g.:

```json
{"farcd": "ok", "mediamtx": "error: connection refused"}
```

Retrying the *identical* request is idempotent: `apid` checks what
already exists on each side and only performs whatever part didn't
complete yet (e.g. "farcd channel already exists with this id, skip;
mediamtx path missing, create it"). This is a deliberate simplification —
no saga/outbox pattern, no distributed transaction — chosen because nothing like that exists elsewhere in this project and a real rollback across
two independently-owned systems is more machinery than this feature
warrants.

## Explicitly out of scope for this ticket

- Any read-side proxying (`GET /channels`, WS journal/event feed) — the
  web app keeps talking to farcd directly for reads, per spec.md's
  "Reads stay direct to farcd" decision.
- Anything beyond channel CRUD — `apid` is deliberately named generically
  because more functionality is expected to land here later, but none of
  it is designed yet.

## Comments
