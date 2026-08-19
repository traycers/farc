# Player page redesign: multi-channel view with a video-presence timeline

Status: design agreed (2026-08-18, via `/mattpocock-skills:grilling`) — not yet implemented

## Problem

The current `web/src/pages/PlayerPage.tsx` supports exactly one channel:
numeric channel-ID input, one `from`/`to` range, a plain HTML table of
`candidates` rows (`begin`/`end`/`play`), one `<video>` element with one
`Hls` instance. There is no visual timeline, no multi-channel view, and no
way to see at a glance where a channel actually has recorded video over a
time range.

The user wants a redesign: pick several channels via checkboxes, search a
time range once, see a per-channel timeline of recorded video segments, and
watch 1/2/N channels at once with a single synchronized playhead — modeled
on typical multi-camera NVR/archive players.

## Reference materials

Provided by the user during grilling (not copied into the repo):
- `timeline_example.png` (`~/Загрузки/timeline_example.png`) — example of a
  multi-channel timeline widget: one row per channel, colored rectangles
  along a shared time axis. The multi-color coding in that reference
  (blue/red/green per state) does **not** apply to farc — see Design
  decisions below.
- `page_player.drawio.png` (`~/Загрузки/page_player.drawio.png`) — wireframe
  of the new page layout: left column (channel checkbox list, search
  params), center (video layout area, current-time indicator, timeline,
  playback buttons), right column ("Виды раскладок" — illustrative only,
  not part of the actual UI, see Q14).

## Design decisions (grilling)

### Timeline semantics
- **One color only.** farc has no per-segment classification data (no
  motion/event/error distinction at this level), unlike the reference
  image's multi-color coding — every rectangle is the same single color.
- **What "recorded" means**: presence of **video** frames only (not audio,
  not "video or audio"). A channel that only had audio in some window shows
  no rectangle there.
- **Merging rule**: consecutive video frames merge into one rectangle if
  the gap between them is < 2s (reusing `vaablocks.GapThresholdNS`); a gap
  ≥ 2s starts a new rectangle. This mirrors `internal/vaablocks.Compute`'s
  existing algorithm — see the hls-server issue for reuse details.
- **fblock boundaries**: it's acceptable for two rectangles from different
  fblocks to sit flush against each other with no visible gap and no
  visible divider — the algorithm is not required to merge across fblock
  boundaries (matches how `vaablocks.Compute` already only ever sees one
  fblock's TOC at a time).
- **No internal dividers**: a merged run of several underlying fcontainers
  renders as one seamless rectangle — no separator lines between the
  original records inside it.
- **No underlying record IDs needed in the response**: playback is done via
  the existing `GET /channels/{channel}/hls/{t1}/{t2}/playlist.m3u8` route
  (channel + time range only) — the timeline never needs to expose
  fcontainer UUIDs for playback to work.

### Data source / architecture
- The new endpoint lives on **hls_server** (`internal/hlsapi`), not farcd.
- hls_server already ingests each fcontainer's TOC via WS
  (`internal/tocindex.EventSubscriber`, per-channel subscription with
  `include_toc=true`) — the video-presence computation should happen right
  when a TOC is decoded there (`indexContainer`), not on-demand per
  request.
- Reuse `internal/vaablocks`'s gap-merge algorithm
  (`GapThresholdNS`/the `Compute` scan logic) for `KindVideo`, but without
  its msm-specific baggage (`Offset`/`Size`/`ConfigID`/`StreamID` are not
  needed for a presence-only timeline).
- One request covers a **list of channels** at once (not one request per
  channel) — the frontend needs several rows rendered together and the
  data is already precomputed, so batching is nearly free.
- Bounded by the same retention window `tocindex` already covers (full
  storage catalog rescanned on `bootstrap()`/reconnect) — no separate
  retention policy needed.

See `.scratch/player-redesign/issues/01-hls-server-timeline-endpoint.md`
for the implementation ticket.

### Page layout and interaction
- **Full replacement** of `PlayerPage.tsx` — no parallel old/new page, the
  old one is a strict subset of the new one's capability (single channel ==
  new page with exactly one checkbox ticked).
- **Left column**: channel list with checkboxes (new component — no
  existing component in `web/src/` does this); search params (`start`,
  `end`, a "Поиск" button). Ticking a checkbox alone loads nothing; only
  pressing "Поиск" fetches the timeline + sets up playback for every
  checked channel, for the given `[start,end]`.
- **Layout is fully automatic**, not user-selectable: 1 checked channel →
  single view; 2 → side-by-side 1×2 (not a 2×2 grid); 3+ → an `N×N` grid
  where `N = ceil(sqrt(count))`, with unused cells left empty. The
  wireframe's right-hand "Виды раскладок" panel was illustrative only for
  this design conversation and is **not** part of the shipped UI.
- **One shared playhead** across every visible channel — a single
  timeline/current-time/play/stop/prev/next control set, not one per
  channel. "Текущее время" is a read-only indicator (not editable/typeable
  — clicking the timeline is the only way to seek).
- **Gap behavior**: when playback reaches the end of the currently loaded
  segment for a channel, if that channel currently has a gap but at least
  one other visible channel still has video at that timestamp, playback
  keeps advancing (channels in a gap show an empty/placeholder frame).
  Only when *every* visible channel is simultaneously in a gap does
  playback jump — to the start of the nearest next segment among any
  visible channel — and continue playing automatically (no user click
  needed to resume across a gap).
- "Предыдущая/следующая запись" buttons jump between these same merged
  timeline segments (the ones actually drawn on screen), not between raw
  fcontainers hidden inside a merged run.
- **Audio**: muted by default on every tile; clicking a tile makes it the
  "active" one and unmutes it (only one active/audible tile at a time).
- No live-viewing mode — this is strictly an archive player over a
  user-chosen fixed `[start,end]` range, same as today.

See `.scratch/player-redesign/issues/02-player-page-redesign.md` for the
implementation ticket.

## Implementation order

01 (hls_server timeline endpoint) has no dependency on 02 and should land
first — 02's frontend work consumes its response shape directly.

Both issues shipped 2026-08-18. All 4 e2e specs (`task test/e2e`) are
green, including the two new ones for this feature — see issue 02's "e2e"
section for a real, pre-existing e2e-harness bug (unrelated to this
feature) found and fixed along the way: `e2e/tests/setup.ts` was still
written against the pre-2026-08-14 one-Filler-per-channel closing model,
which stopped matching reality once fblock closing became purely
fullness-driven.

## Testing strategy (grilling, 2026-08-18)

- **Issue 01**: no separate plan needed — the ticket already pins down the
  algorithm, integration point, and unit-test seams; go straight to
  `/mattpocock-skills:tdd`.
- **Issue 02**: draft a short implementation plan first (component
  boundaries for the shared playhead / grid layout / gap-skip state
  machine) before TDD — real architectural choices here, not mechanical.
- **e2e**: two new Playwright scenarios under `e2e/` (see issue 02's "e2e
  coverage" section) — one for real multi-channel simultaneous playback,
  one using an event-policy channel (`POST /channels/{id}/events`, already
  exercised by `e2e/tests/journal.spec.ts`) to produce a real >2s gap and
  confirm the timeline splits it into two segments with automatic
  playhead advance across it. The gap-skip *logic itself* stays unit-
  tested on synthetic data (per Q2) — the e2e scenario is an added
  integration check, not the primary way that logic gets verified.
  Nothing added to the lighter `go test -tags e2e ./tests/...` suite —
  redundant with the Playwright scenario for this feature.
