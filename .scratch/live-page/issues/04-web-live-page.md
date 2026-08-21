# Web: new Live page

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

A new `/live` page: left-hand channel list with live status + grid
membership, right-hand live-video grid, and a one-click jump into the
archive player for a specific channel over the last hour.

## Scope

- New route `/live` (`web/src/App.tsx`), new nav link (after "Player",
  before "Журнал" — matches the existing Storages/Channels/Player/
  Журнал ordering by workflow position: setup → setup → watch archive →
  watch live → audit log... actually place "Live" right after "Player"
  since both are viewing pages; exact final ordering is not
  load-bearing, pick something reasonable).
- New page component, e.g. `web/src/pages/LivePage.tsx`.
- **Left column**: one row per channel from `listChannels()` — id,
  `ChannelStatusIndicator` (`issues/05-shared-status-indicator.md`),
  name, a checkbox meaning "show this channel in the grid on the right",
  and a separate link/button ("в архив" or similar) that navigates to
  `/player?channel={id}` (`issues/06-player-query-params.md`) — this must
  be a distinct clickable element from the checkbox/row, since the
  checkbox already claims the row-click semantics.
- Checkbox state persists in `localStorage`, restored on page load;
  empty on the very first visit. Do not default to "all checked".
- **Right column**: reuse `VideoGrid`'s existing auto-layout logic
  (`web/src/components/VideoGrid.tsx` — 1/2/N×N by count of checked
  channels), fed the checked channel ids. Each tile is a **new**
  component (not `VideoTile`, which is hls.js/VOD-specific) that opens a
  WebRTC (WHEP) connection to the URL returned by `apid`'s batch
  live-URL lookup (`GET /channels/live-urls?ids=...`,
  `issues/01-apid-server.md`) for that channel.
- Mirror `PlayerPage`'s audio model: clicking a tile makes it the
  "active" one (unmuted); all other tiles stay muted. No audio from more
  than one tile at once.
- A tile with no established WebRTC connection (WHEP failed, or mediamtx
  has no active source for that path) shows a "нет сигнала"-style
  placeholder — this is unrelated to farcd/`hlsd`'s own state; do not
  wire it to `last_connect_error` or any archive/HLS signal.
- Batch-fetch live URLs once per change to the checked-channel set (not
  once per channel) — see `issues/01-apid-server.md`'s batch endpoint.

## Suggested improvements (non-blocking, explicitly requested)

- Sort the left-hand list with disconnected/not-recording channels
  first, for faster triage.
- A soft warning if an unusually large number of channels get checked at
  once (each is a live WebRTC session).

## Comments
