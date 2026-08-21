# Live page: per-channel live video + status, backed by a new `apid` + mediamtx

Status: design agreed (2026-08-21, via `/mattpocock-skills:grilling`) — not yet implemented

## Problem

There is no way to see live video from a channel today — `hls_server`
only serves bounded-range archive (VOD) playlists built from finalized
fcontainers (`docs/docs/archive/12-hls-server.md`), and closing an
fcontainer is byte-fullness-driven, not time-driven
(`internal/storage/segment.go`'s `isFullLocked`), so pointing the existing
player at a rolling "now" window would have unpredictable multi-minute
delay on low-activity channels — not acceptable for a live-monitoring
page.

The user wants a new **Live** page: a left-hand list of channels (id,
two-circle status indicator, name, a checkbox for "show in the grid on
the right", a link to jump into the archive player for this channel), and
a right-hand video grid — reusing `PlayerPage`'s `VideoGrid` layout model
(1/2/N×N auto-arranged by number of checked channels) — showing real live
video for the checked channels.

## Reference materials

None provided beyond the grilling conversation itself.

## Design decisions (grilling)

### Live video source: mediamtx over WebRTC, not hls_server

- Real live video (not archive-with-latency) requires a component built
  for it. **mediamtx** (already used as an RTSP test-source generator in
  `docker-compose.yaml`/`e2e/docker-compose.e2e.yaml`, dev/e2e-only today)
  becomes a real production component: it pulls RTSP from the camera and
  serves WebRTC (WHEP) for the Live page's video tiles.
- `hls_server` (renamed `hlsd`, see `.scratch/hlsd-rename/`) continues to
  serve **only** the archive player (`PlayerPage`) — no fallback to it
  from the Live page's video tiles if WebRTC fails. "No signal" on a Live
  tile means the WHEP connection didn't establish or mediamtx has no
  active source for that path — it does not mean anything about
  `hlsd`/farcd.
- **Single RTSP connection to the camera**: mediamtx is the *only* thing
  that connects to the camera's RTSP stream. farcd's own ingest
  (`internal/ingest`, `gortsplib`) connects to **mediamtx's re-served RTSP
  output** for that channel, not to the camera directly. This is a
  deliberate topology change from today (where a channel's `rtsp_url` in
  farcd is the camera's own address) — see `apid`'s create flow below.
  Consequence: farcd's own `recording`/`last_connect_error` health state
  (already exposed via `GET /channels` and the WS journal) is, after this
  change, an accurate proxy for "is there a live signal at all" too,
  since farcd can only be connected if mediamtx is successfully relaying
  the camera — no separate mediamtx-side health check is needed for the
  status indicator (see below).
- mediamtx config must have WebRTC enabled (explicit user requirement).

### New component: `apid`

- A new Go binary, **`apid`** (`cmd/apid/`), living in this same
  monorepo, alongside `farc` (farcd) and `hls_server`/`hlsd` — same reason
  those two already live together: it talks to both farcd and mediamtx
  and belongs in the same docker-compose stack.
- **Not** an extension of farcd — explicit user constraint ("не стоит
  расширять farcd"), because more non-channel functionality that must not
  live in the web client is expected to land in `apid` over time. Hence a
  generic name, not `channel_server`.
- Config follows the existing split convention (`internal/config`,
  `internal/hlsconfig`): env vars for its own server address + the
  farcd/mediamtx addresses it talks to, a JSON file (via a new
  `internal/apiconfig`, with `Save`/`EnsureExists` like the other two) for
  anything site-specific.
- **Scope**: `apid` owns the full write path for channels — create,
  update, remove. The web app no longer calls farcd's `/channels` REST
  routes directly for writes; it calls `apid`. Reads (`GET /channels`,
  the WS journal/event feed for recording/connect status) stay exactly as
  they are today, direct to farcd — no read-side proxying, no benefit to
  adding a network hop for a path that already works and has no
  cross-service orchestration to do.
- **Create-channel flow** (`POST` to `apid`, called from `ChannelNewPage`):
  1. Input: channel metadata (name, capture policy, etc.) + the **camera's
     real RTSP URL** (same field the web form collects today).
  2. `apid` creates a mediamtx path (REST config API, e.g.
     `/v3/config/paths/add/{name}`) with the camera URL as source. Path
     name = the channel id (stringified) — simple, unique by construction
     (farcd channel ids are already unique), no separate naming scheme to
     invent.
  3. `apid` creates the farcd channel via farcd's existing `POST
     /channels`, but with `rtsp_url` rewritten to mediamtx's own re-served
     RTSP address for that path (e.g. `rtsp://<mediamtx-host>:8554/{channel_id}`)
     — **not** the camera URL the user typed in.
  4. `apid` records (in its own JSON config, or derives on demand — either
     is fine) whatever it needs to answer "what's the WHEP URL for
     channel N" later.
  - **Update**: if the camera RTSP URL changes, `apid` updates the
    mediamtx path's source and leaves farcd's `rtsp_url` (which points at
    mediamtx, not the camera) untouched. Other field changes (name,
    capture policy, prerecord/postrecord) pass through to farcd only.
  - **Remove**: `apid` removes the farcd channel and the mediamtx path.
  - **Partial-failure handling**: no rollback/saga. If one of the two
    downstream calls (farcd, mediamtx) fails, `apid` returns which part
    succeeded/failed (e.g. `{"farcd": "ok", "mediamtx": "error: ..."}` )
    and leaves the successful side as-is. Retrying the *same* create/
    update/remove request is idempotent — it only does whatever part
    didn't complete yet. Rationale: no saga/outbox infrastructure exists
    in this project, and a real distributed rollback across two
    independently-owned systems is a bigger investment than this feature
    needs; idempotent retry is simpler and doesn't discard already-good
    state on a purely-mediamtx-side hiccup.
- **Live-view lookup endpoint**: `apid` exposes a **batch** endpoint (one
  request for a list of channel ids → their WHEP URLs), not one round
  trip per channel — the Live page's grid needs this for every checked
  channel at once.
- **Known constraint to document, not solve here**: many IP cameras cap
  concurrent RTSP sessions; this design assumes one (mediamtx's) is
  enough — that's the whole point of routing farcd through mediamtx
  instead of both connecting to the camera independently.

### Status indicator: shared two-circle component

- New shared component (`web/src/components/ChannelStatusIndicator.tsx`
  or similar) — an **outer** ring (farcd's own RTSP connection status:
  blue = connected, red = `last_connect_error` set — same field that
  exists today) and an **inner** dot (recording vs idle — identical to
  today's `.status-dot`/`.status-dot-recording`/`.status-dot-idle` on
  `ChannelsIndexPage`).
- `ChannelsIndexPage` is refactored to use this shared component too,
  replacing its current inline `<span className="status-dot ...">` +
  separate `last_connect_error` text block — single visual language for
  channel status across both pages. (The connect-error *text* and the
  page-level `connectFailedBanner` can stay; only the status-cell markup
  changes to render the shared component instead of just the inner dot.)
- No mediamtx-side health signal is folded into this indicator — see
  "Single RTSP connection to the camera" above for why farcd's own status
  is now an adequate proxy.

### Live page layout and interaction

- **Left column**: one row per channel — id, `ChannelStatusIndicator`,
  name, a checkbox ("show in the grid"), and a separate link/button to
  jump into the archive player for this channel (distinct control from
  the checkbox — the checkbox is claimed for grid membership, so it can't
  also mean "open player").
- Checkbox state persists in `localStorage` across visits (keyed however
  is natural, e.g. by a fixed key holding the set of channel ids) — empty
  on first-ever visit, restored on subsequent ones. Rejected: "all
  checked by default" (an unbounded number of simultaneous WebRTC
  sessions on every page load) and "always empty" (annoying for a page
  people return to watch the same channels on).
- **Right column**: reuse `VideoGrid`'s auto-layout (1/2/N×N by count of
  checked channels, `web/src/components/VideoGrid.tsx`), but each tile is
  a **new** live-video tile component (WebRTC/WHEP client connecting to
  the URL `apid` returns for that channel) — not `VideoTile` (which is
  hls.js-based, VOD-only). As in `PlayerPage`, only the clicked/"active"
  tile is unmuted; the rest play muted (mirrors `VideoTile`'s existing
  `active`/`muted`/`onClick` props — same interaction model, new
  underlying transport).
- **Jump to Player**: clicking the per-row link navigates to
  `/player?channel={id}`. `PlayerPage` gains support for a `channel`
  query param: pre-check that channel in `ChannelChecklist`'s state, and
  — since the default `from`/`to` is already "now − 1h → now" — call
  `onSearch` automatically on mount when the param is present, instead of
  requiring an extra manual "Search" click. See
  `issues/06-player-query-params.md`.

### Suggested improvements (asked for explicitly, not blocking)

- Sort the left-hand channel list so disconnected/not-recording channels
  float to the top — faster triage on a monitoring page.
- Warn (non-blocking) if an unusually large number of channels get
  checked at once, given each is a live WebRTC session.

## Naming

- New binary: **`apid`** (not `channel_server`/`channel_gateway` —
  rejected because more non-channel functionality is expected to land
  here later, and a channel-specific name would be misleading).
- `hls_server` → `hlsd` rename: tracked entirely separately, see
  `.scratch/hlsd-rename/spec.md` — unrelated to this feature other than
  sharing the `<name>d` binary-naming convention `apid` also follows.

## Issues

- `issues/01-apid-server.md` — new `apid` binary: config, farcd+mediamtx
  channel-CRUD orchestration, batch WHEP-URL lookup, idempotent
  partial-failure semantics.
- `issues/02-mediamtx-production-deploy.md` — mediamtx as a real
  production component (not just dev/e2e): docker-compose service, WebRTC
  enabled, reachable by both `apid` (REST config) and browsers (WHEP).
- `issues/03-web-channel-writes-via-apid.md` — `ChannelNewPage`/
  `ChannelEditPage`/remove flow switch from farcd to `apid`; nginx +
  vite dev-proxy updates.
- `issues/04-web-live-page.md` — the Live page itself.
- `issues/05-shared-status-indicator.md` — extract
  `ChannelStatusIndicator`, adopt it on `ChannelsIndexPage`.
- `issues/06-player-query-params.md` — `PlayerPage` gains `?channel=` +
  auto-submit.
- `issues/07-docs-and-adr-update.md` — `12-hls-server.md` no longer
  excludes live viewing from farc's scope; new ADR for the `apid`/mediamtx
  topology; `PLAN.md`/`CLAUDE.md` updates.
