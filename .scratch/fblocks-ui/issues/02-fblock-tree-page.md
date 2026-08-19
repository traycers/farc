# fblock-tree page: live in_progress support, collapse/depth-5, client-side frame grouping; retire old live-tree

Status: resolved

## Problem

Today, two separate pages exist for viewing a fcontainer's tree:

- `FblockStatusPage` (`/storages/:id/fblocks/:index/status`) — a single
  fblock's TOC tree, but **only for `ready`** fblocks, no collapse/expand,
  always fully rendered flat.
- `StorageLiveTreePage` (`/storages/:id/live-tree`) — the **whole
  storage's** in-progress data, merged across every recording channel via
  `internal/api/fblocktree.go`'s `mergeLiveTrees`/`remapTreeIDs`. Too big/
  unwieldy (this is the concrete complaint that started this effort) and,
  since `.scratch/multi-channel-fcontainer/issues/02` landed the same day,
  now solving a problem (per-channel trees needing to be stitched
  together) that no longer exists — channels of one Storage already share
  one segment, so one coherent tree is directly available.

Neither page supports collapsing nodes, and neither groups the (very
numerous) frame nodes, making large trees impractical to read.

## Design (settled via grilling, see `../spec.md` for the full decision
list — key points repeated here for this ticket's scope)

- This ticket **extends `FblockStatusPage`**, it does not add a third
  page. Rename the route from `.../status` to `.../tree` (resolves the
  naming collision with `issues/01`'s "fblocks status" grid-page label —
  flagged during grilling as non-blocking but worth fixing) and the file
  to `FblockTreePage.tsx`.
- Supports both `ready` (static, from `ReadTOC`) and `in_progress` (live)
  fblocks. `bad`/`uninitialized` show an error/empty state as today.
- Default expand depth 5 (root = depth 0, levels 0-4 open), plus
  "expand all"/"collapse all" buttons.
- Client-side-only frame-node grouping: runs of 100 (video) / 500 (audio)
  consecutive sibling frame-role nodes collapse into one clickable
  summary row, expandable back to individual frames. Pure frontend
  transform over the already-fetched/streamed tree — no farcd changes.
- `StorageLiveTreePage` route, its nav entry, and its entire backend
  (`handleStorageLiveTreeWS`, `buildStorageLiveTreeMessage`,
  `mergeLiveTrees`, `remapTreeIDs`, `storageLiveTreeMessage`,
  `channelLiveSig`/`liveSigsEqual`, `channelRecording`/
  `channelPreviousRef`/`previousFblockRef`/`resolvePreviousFblock`) are
  deleted. `buildLiveTree`/`liveNode` are kept (reused by the new
  endpoint below). Frontend: `StorageLiveTreePage.tsx`,
  `subscribeStorageLiveTree`/`StorageLiveTreeMessage` in
  `web/src/api/fblockTree.ts` removed.

### New backend: per-fblock live endpoint

Only one fblock per Storage can ever be `in_progress` at a time (Pool's
FIFO head is the sole occupant with a physical index — see
`internal/storage/pool.go`'s doc comments). So "is fblock `{index}` live
right now" is answerable purely from the existing catalog snapshot
(`state(idx) == fblock.InProgress`), with no need to cross-reference
`Pool`/`Segment` internals from `internal/api` at all:

- New `IngestManager` method (`internal/ingest/ingestmanager.go`),
  something like `LiveTreeForStorage(storageID string) (elems
  []mediatree.Element, generation uint64, ok bool)` — reads directly from
  the shared `*StorageSegment` for that storage (`m.storageSegments[...]`),
  no per-channel loop, no merge.
- New handler, e.g. `GET /storages/{id}/fblocks/{index}/tree/ws`:
  1. Resolve `idx`; check `snap.State(idx) == fblock.InProgress` (else
     404/error — "not live").
  2. Call `s.ing.LiveTreeForStorage(id)`, build via existing
     `buildLiveTree(elems)`.
  3. Same poll-and-resend-on-change pattern as the old handler (500ms
     tick, change-detected via a `(generation, elemCount)` signature) —
     but now storage-scoped with a single signature, not a per-channel map.
  4. No `recording`/`previous` fields — those belonged to the old page's
     whole-storage multi-channel UX; the per-fblock page doesn't need
     them.
- `handleReadFblockTree` (the existing `ready`-fblock endpoint) is
  unchanged.

### Frontend

- `FblockTreePage.tsx` (renamed from `FblockStatusPage.tsx`): on mount,
  `getFblockInfo` -> branch on `state`:
  - `ready`: `getFblockTree(id, uuid)` (existing, unchanged).
  - `in_progress`: new `subscribeFblockLiveTree(storageId, index,
    onMessage)` (new WS client fn in `fblockTree.ts`, modeled on the
    deleted `subscribeStorageLiveTree` but scoped by index and with the
    simpler `{tree}`-only message shape).
  - other states: existing error path.
- New pure function (e.g. `groupFrameNodes(root, {video: 100, audio:
  500}) => TreeNode`) applied to the fetched/streamed tree before
  rendering — needs the exact frame-node `Role` values from
  `mediatree.Role` (video vs. audio frame nodes) to know which siblings
  are groupable; confirm exact role names during implementation.
- `FblockTree` component gains collapse/expand state (per-node,
  default-open-if-depth<5) and expand-all/collapse-all controls — today
  it has neither (`web/src/components/FblockTree.tsx` currently always
  renders everything).

## Scope (for /plan)

- Exact new WS route path/name (`.../tree/ws` above is a proposal, not
  fixed).
- Exact `mediatree.Role` values that mark a "frame" node groupable, and
  whether grouping applies to the immediate children of a stream node or
  some other level of the tree — confirm by reading `mediatree/type.go`/
  `07-media-tree.md` during implementation.
- Collapse-state representation (e.g. a `Set<nodeId>` of manually-toggled
  nodes, depth computed once at render time for the default) — internal
  to `FblockTree`, not exposed in this ticket's text.
- Test seams: `internal/ingest` (new `LiveTreeForStorage` method, unit
  test with the existing `fakeSegmentBackend`/`newTestSegment` fixtures
  from `.scratch/multi-channel-fcontainer`), `internal/api` (new handler,
  httptest against a running Unit+IngestManager, mirroring
  `fblocktree_test.go`'s existing conventions if any), `web/src`
  (`groupFrameNodes` pure-function unit tests; `FblockTree` collapse/
  expand-depth behavior).

## Docs

- `docs/docs/archive/11-service-composition.md`/`12-hls-server.md` don't
  reference the admin live-tree page (confirm during implementation) —
  if any doc describes the old whole-storage live-tree UX, update it.

## Comments

Implemented (2026-08-14): `internal/ingest/ingestmanager.go`'s
`LiveTreeForStorage(storageID)` reads the shared `StorageSegment` directly
(no per-channel merge). `internal/api/fblocktree.go`'s
`handleFblockLiveTreeWS` (`GET /storages/{id}/fblocks/{index}/tree/ws`)
gates on `state==in_progress` from the catalog snapshot alone (only the
Pool's FIFO head is ever `in_progress`, so no `Pool` internals needed in
`internal/api`), reusing the existing `liveTreeUpgrader`/poll-interval
constants. Old whole-storage machinery deleted entirely:
`handleStorageLiveTreeWS`, `buildStorageLiveTreeMessage`, `mergeLiveTrees`,
`remapTreeIDs`, `storageLiveTreeMessage`, `channelLiveSig`/`liveSigsEqual`,
`channelRecording`/`channelPreviousRef`/`previousFblockRef`/
`resolvePreviousFblock` — confirmed via `LiveSnapshot()`/`filterChannelElements`
call-site grep that `internal/ingest`'s own per-channel `LiveSnapshot` API
stays untouched (still actively tested, just no longer this page's route
to the data). Frontend: `FblockStatusPage.tsx` renamed to
`FblockTreePage.tsx` (route `.../status` -> `.../tree`, resolving the
naming collision with ticket 01's "fblocks status"), branches on
`ready`/`in_progress` state; new `subscribeFblockLiveTree` (`fblockTree.ts`,
replacing `subscribeStorageLiveTree`). New `web/src/api/frameGrouping.ts`
(`groupFrameNodes`, TDD'd with vitest) groups runs of 100 `frame(video)` /
500 `frame(audio)` siblings into a collapsible synthetic node. `FblockTree.tsx`
gained collapse/expand (default depth 5, root=depth 0; a frame-group node
always starts closed regardless of depth) plus "expand all"/"collapse all"
controls, also TDD'd. `StorageLiveTreePage.tsx` and its nav entry deleted.
Full backend+frontend suites green (`-race` clean on `internal/api`/
`internal/ingest`), `golangci-lint run` clean, `tsc -b`/`vite build` clean.
`CONTEXT.md`'s **fcontainer** entry corrected (described the now-removed
page as current).
