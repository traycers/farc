# web: redesign PlayerPage as a multi-channel view with a timeline

Status: fixed (2026-08-18, via `/mattpocock-skills:tdd`, following the
approved plan at `.claude/plans/fluttering-sleeping-canyon.md`) — frontend
implemented and both e2e scenarios green against the real Docker stack

Depends on: 01 (hls_server timeline endpoint) — needs its response shape to
render the timeline rows.

See `.scratch/player-redesign/spec.md` for the full design conversation
this was split from.

## Goal

Replace `web/src/pages/PlayerPage.tsx` entirely with a page that:

- Lists channels with checkboxes (new left-column component — nothing
  reusable exists in `web/src/` today; `ChannelsIndexPage.tsx` is a CRUD
  table, not a multi-select list).
- Has search params (`start`, `end`, a "Поиск" button) that, on submit,
  fetch: the new timeline endpoint (01) for every checked channel over
  `[start,end]`, and set up playback.
- Renders one row per checked channel on a shared timeline (single color
  per rectangle, merged per the rules in spec.md — this page just draws
  what `01`'s endpoint returns, no merge logic on the frontend).
- Automatically lays out the video area based on checked-channel count: 1 →
  single view, 2 → side-by-side (1×2), 3+ → `N×N` grid with
  `N = ceil(sqrt(count))` (empty cells left blank). No manual
  layout-picker UI.
- Drives all visible `<video>`/`Hls` instances from **one shared
  playhead** — one timeline, one current-time indicator (read-only, not
  editable), one set of play/stop/prev/next controls for the whole page,
  not per channel.
- Implements the gap-skipping behavior from spec.md: keep playing as long
  as at least one visible channel has video at the current playhead time
  (channels without video at that instant show an empty/placeholder
  frame); when every visible channel is simultaneously gapped, jump
  forward to the nearest next segment among any visible channel and keep
  playing, with no user click required.
- "Предыдущая/следующая запись" navigate between the timeline's own
  (already-merged) segments.
- Playback for each visible channel still goes through the existing
  `GET /channels/{channel}/hls/{t1}/{t2}/playlist.m3u8` +
  `/segments/...` routes (channel + time range, no fcontainer UUIDs
  needed) — this ticket doesn't touch that API.
- Audio: every tile starts muted; clicking a tile makes it the sole
  "active"/audible one.

## Fix

Implemented exactly per the approved plan's vertical slices, each TDD
red→green:

- `web/src/api/ns.ts`: extracted `quoteBigintFields(text, fields)` out of
  `parseCandidatesJSON`, no behavior change; new `ns.test.ts`.
- `web/src/api/hls.ts` (new): `getTimeline`/`playlistUrl`/`Segment`/
  `ChannelTimeline`, parsing the batch timeline response via
  `quoteBigintFields`.
- `web/src/pages/playerLayout.ts` (new): `gridShape`/`layoutCells` — 1→1×1,
  2→1×2 (explicit exception), 3+→`ceil(sqrt(count))²` with trailing cells
  `null`.
- `web/src/pages/timelineGeometry.ts` (new): `segmentToRect`/`fractionToNs`
  — the click-to-seek/paint math, kept separate from playback semantics.
- `web/src/pages/playerTimeline.ts` (new): `isAliveAt`/`nextSegmentStart`/
  `prevSegmentStart`/`advance` — the gap-skip core, a pure function of
  `(timelines, t)`, exhaustively unit-tested independent of any UI.
- `web/src/components/ChannelChecklist.tsx`, `TimelineBar.tsx`,
  `VideoTile.tsx`, `VideoGrid.tsx` (all new): thin renderers per the plan's
  split, each with its own test (`TimelineBar.test.tsx` includes the
  `getBoundingClientRect`-stubbed click-to-seek case and the direct-child,
  no-intermediate-wrapper DOM-shape assertion per issue 11's lesson).
- `web/src/pages/PlayerPage.tsx`: fully rewritten — orchestrates the above,
  owns the single `playheadNs`/`playing` state, a `setInterval`-driven tick
  effect calling `advance()` each 200ms, "Стоп" pauses in place. `App.tsx`
  needed no change (`/player` already routed to this file's default
  export).
- `web/src/index.css`: `.player-*` classes (timeline row/segment/cursor as
  direct `position:absolute` children of `position:relative` parents, no
  intermediate wrapper; a `.player-page` override widening `main` past its
  960px default for a 3×3+ grid).

**Debugging note worth keeping**: the gap-skip wiring test
(`PlayerPage.test.tsx`) initially failed because a literal
`'1970-01-01T00:00'` string fed to a `datetime-local` input is parsed by
`nsFromLocalInputValue` in the *test runner's local timezone*, not UTC —
in this environment (UTC+3) that produced a negative `t1`, nowhere near the
fixture's small ns-scale segments. Fixed by generating the field value via
`nsToLocalInputValue(0n)` (the same function the page itself uses), which
round-trips correctly regardless of the runner's timezone.

**Verified**: `npx vitest run` (72 tests, all packages), `npx tsc --noEmit`,
and `npm run build` (full `tsc -b && vite build`) all clean.

## e2e: a real, previously-latent bug found and fixed along the way

Running the two new e2e scenarios against the real `task test/e2e` Docker
stack surfaced a genuine, pre-existing bug **unrelated to the player
redesign**: `e2e/tests/setup.ts` (backing `two-channel-playback.spec.ts`)
called `POST /channels/{id}/recording/stop` and waited for a confirmed
candidate, which never arrives. Root cause: the 2026-08-14/15
multi-channel-fcontainer refactor (`.scratch/multi-channel-fcontainer/
issues/02-ingest-shared-filler-per-storage.md`, "Close dynamics") made
fblock closing (`In Progress` → `Ready`) **purely fullness-driven** — a
storage's active fblock is written by one Filler shared across every
channel assigned to it, and neither an explicit `recording/stop` nor an
event channel's `postrecord_ns` auto-stop (`internal/ingest/policy.go`'s
`closeSegmentLocked`, called from both paths) closes that shared Filler
for anyone — only `internal/storage/segment.go`'s fullness check
(`isFullLocked`/`closeForFullnessLocked`) does. `setup.ts`'s own comments
still described the pre-refactor one-Filler-per-channel model, where
`StopRecording` was what flushed to disk; that stopped being true once the
refactor landed, and nothing had exercised this specific test path since.

Fix: rewrote `setup.ts` to mirror `continuous-rotation.spec.ts`'s already-
correct pattern — start both channels recording and poll
`GET /storages/{id}/fblocks/0` for `ready` (fullness-driven, combined
across both channels), no explicit stop needed to reach `Ready` at all;
added a `teardownStack()` export (stop + remove both channels) called from
the spec's `afterAll` so they don't keep consuming mediamtx/CPU for the
rest of a sequential suite run. Also added the same `afterAll` cleanup to
`continuous-rotation.spec.ts` (its channel 95 was never removed before,
just left recording forever, which — combined with `playwright.config.ts`
lacking `workers: 1` — starved later specs' real RTSP recordings of
resources once a 4th real-media spec existed).

For the new `player-gap-skip.spec.ts`, the same fullness-only-close
constraint meant `FBLOCK_SIZE` had to be deliberately small enough that two
real triggered bursts' *combined* content crosses fullness sometime during
the second burst, landing both (and the real idle gap between them) in one
fblock's TOC — landed on `12 MiB`/`15s` postrecord per burst after an
initial `4 MiB` attempt closed during burst 1 alone (a small fblock's fixed
per-block overhead — prologue/catalog/header-checksums/epilogue — turned
out to dominate its real usable content budget, rotating through all 4
fblocks in a few seconds instead of behaving proportionally to bitrate).
The test also polls hls_server's new `/timeline` endpoint directly
(bigint-safe, same parsing approach as `web/src/api/hls.ts`) rather than
farcd's coarser `candidates` (which wouldn't show the internal gap at all),
and asserts `segments.length >= 2` rather than exactly 2, tolerating an
incidental extra fblock-boundary split without weakening what the test
actually proves.

`playwright.config.ts` also gained `workers: 1` — `fullyParallel: false`
only serializes tests *within* one spec file, not different files sharing
this suite's one real, finite mediamtx/farcd stack.

**Final verification** (`task test/e2e`-equivalent, run manually against
this sandbox's Docker — see the sandbox-specific `docker run` invocation
used to reach the compose network directly, since a transparent proxy
intercepts host→`localhost` traffic here): all 4 specs green —
`continuous-rotation.spec.ts`, `journal.spec.ts`,
`two-channel-playback.spec.ts`, `player-gap-skip.spec.ts`.

## Explicitly out of scope

- Live viewing (only fixed archive ranges).
- A manual layout picker (the wireframe's "Виды раскладок" panel was for
  communicating the design, not a UI element to build).
- Typing an exact time into the current-time field.
- Multi-color/typed timeline segments (no data source for that today).

## Plan first

Unlike issue 01, this one has a real architectural fork (component
boundaries for the shared playhead, grid layout, and gap-skip state
machine) that should be drafted as a short implementation plan (e.g. via
Plan mode) before starting TDD — so the seams below are chosen
deliberately rather than improvised mid-cycle.

## Tests

Seams to confirm before writing (per the TDD skill) once the component
boundaries are decided — likely candidates: the layout-size calculation
(`count -> grid shape`) as a pure function, the gap-skip/auto-advance
playhead logic as a pure function over a list of per-channel segments, and
the timeline-row rendering given a fixture endpoint response. Don't lock
these in until the component split is actually drafted.

## e2e coverage (grilling, 2026-08-18)

Decided during a second grilling round (see spec.md's "Testing strategy"):

- **New Playwright scenario, multi-channel playback**: tick 2+ channels,
  search, assert the grid renders side-by-side and every visible tile is
  actually playing (this is the one thing unit tests structurally can't
  cover — real synchronized `<video>`/`Hls` instances in a browser).
- **New Playwright scenario, gap auto-skip**: create a test channel with
  `capture_policy: {type: 'event'}` (same pattern as
  `e2e/tests/journal.spec.ts`'s `createChannel`/`triggerEvent` helpers),
  fire two triggers with a gap `>= 2s` between the resulting recordings,
  then assert the new page's timeline shows two separate segments and that
  pressing play advances across the gap automatically (no manual
  next-record click needed). This is an integration-level confirmation on
  top of the gap-skip unit tests, not a replacement for them.
- **Not adding** anything to the lighter `go test -tags e2e ./tests/...`
  suite for the new hls_server timeline endpoint — the Playwright scenario
  above already exercises it live (the timeline wouldn't render without
  it), and unit tests (issue 01) cover the segment-computation logic in
  isolation; a third, browser-less integration test of the same endpoint
  would just duplicate coverage without buying new confidence.
