# Plan (design only): live pool-fullness status list on "fblocks status"

Status: implemented (2026-08-14 to 2026-08-17, via `/mattpocock-skills:tdd`,
five vertical slices — see Comments). No longer plan-only.

## Problem / motivation (user's own framing)

Before the fblock squares on "fblocks status", show a list where each row
is one **pool slot** (not a physical fblock index — the in-memory buffer
pool from `.scratch/multi-channel-fcontainer/issues/01`), so an operator
can see at a glance which buffer is actively writing, which are queued
waiting their turn, which is filling live, and which are free/ready. Row
shape: `[status square][prolog][catalog][content][toc][epilog]`, each
section box sized proportionally to its actual byte size, plus the total
fblock size. Live-updating.

## Design (settled via grilling, 2026-08-14)

1. **Row count**: always exactly `PoolTuning.Size` rows, including free
   (unreserved) slots. Free rows render all 5 section boxes at their
   expected/default sizes, empty/translucent — same visual structure as
   an occupied row, not a visually cut-down shape.
2. **Status-square states** (4, mapped 1:1 to `Pool`/`Segment` internals):
   - *free* — slot not reserved (`Pool.occupied` doesn't include it).
   - *filling: queued* — reserved (`Pool.occupied[i]`, `i>0`), accumulating
     in memory, no physical index yet (not yet `promoteLocked`).
   - *filling: active* — reserved, `Pool.occupied[0]` (FIFO head), has a
     physical index, genuinely `in_progress` on disk.
   - *closing* — final TOC+epilogue write in flight (including the
     existing corruption-triggered retry-on-new-index loop in
     `Segment.Close`).
3. **Section-box sizing**: proportional/stacked-bar to real byte size, not
   fixed-width-with-text-label. Prolog/catalog sizes are fixed once a
   segment is promoted (known from `fblock.ComputeOffsets`); content
   grows as frames are appended (periodic-flush `Written()` bytes, or
   in-memory backlog length pre-promotion); TOC's *live* size — see (4);
   epilog is a fixed constant (`fblock.EpilogSize`).
4. **Live TOC size — exact, not estimated**: confirmed by reading
   `toc/build.go`/`toc/columns.go` that a built TOC's byte size is a pure,
   exact, closed-form function of element count `n` alone: 128-byte
   fixed header+directory, plus 27 bytes/element, plus <=418 bytes of
   bounded alignment padding spread across 6 SoA columns (the padding
   term isn't a flat per-element constant — it's a bounded sawtooth in
   `n`, see the formula in `../spec.md` — but it needs nothing beyond
   `n`: no sorting, no whole-tree structural knowledge, no `Role`/`Value`
   dependence). So: maintain a running element count per open segment
   (already implicitly available — `Filler`/`segmentImpl` already track
   appended elements) and compute the exact current TOC size on demand
   from that count, without ever calling `toc.Build` while filling.
5. **Live-update transport**: reuse the existing per-storage WS event
   feed (`internal/api/eventpush.go`), new event type(s) layered on top —
   not a separate poll loop. (The existing feed already carries fblock
   lifecycle events consumed by the "Журнал" page and today's grid page.)

## What implementing this would actually require (not done here)

- `internal/storage/pool.go`: `Pool` needs a new exported enumeration
  accessor (today only `Status()` — the aggregate level — exists;
  `occupied []poolSlot` is unexported and `poolSlot` itself only exposes
  `promoteLocked`, no state/index/size getters at all). Likely: extend
  what a slot exposes (a richer interface than `poolSlot`, or have
  `Segment`/`segmentImpl` supply this instead) so a caller can enumerate
  "for each occupied slot: state, physical index if any, current content
  bytes, current element count."
- `fblock`/`internal/storage`: no format change needed — prolog/catalog
  sizes are already computable from existing `fblock.ComputeOffsets`;
  epilog size is already a constant. Only new *runtime introspection*
  is needed, not a storage-format change.
- `internal/api`: new WS event type(s) (or an extension of the existing
  per-storage feed's payload) carrying one snapshot of all `PoolTuning.Size`
  slots' state, pushed on change (mirroring the existing 500ms-poll-and-
  diff pattern `issues/02`'s new per-fblock live endpoint also uses, or
  potentially event-driven rather than polled, since Pool state changes
  are already discrete transitions the storage engine could push directly
  instead of being polled for).
- `web/`: new component rendering the row list above the existing grid on
  `FblocksGridPage`/the renamed "fblocks status" page; stacked-bar section
  visualization; state-square styling/legend.

## Comments

Design finalized 2026-08-14 via `/mattpocock-skills:grilling`
(one-question-at-a-time). Deliberately not implemented at design time —
the user asked explicitly for a plan, not code, for this item.

2026-08-14, `/mattpocock-skills:tdd`: implementation started. Seam agreed
with the user: `internal/storage`'s `Pool` gains a public snapshot method,
tested via the package's existing `fakePoolSlot`-based test style
(`pool_test.go`), deferring the WS-event and web layers to later slices.

First vertical slice done, three red→green cycles:
- `Pool.Slots()` — always returns `PoolTuning.Size` entries.
- `poolSlot` interface gained `index() (idx uint32, ok bool)`;
  `segmentImpl.index()` implements it for real (`internal/storage/segment.go`);
  `fakePoolSlot.index()` for tests.
- `SlotState` (`SlotFree`/`SlotQueued`/`SlotActive`) + `SlotStatus{State,
  Index, HasIndex}` — free (unreserved), queued (occupied, non-head, no
  index yet), active (FIFO head, has a physical index).

2026-08-17, `/mattpocock-skills:tdd`: second vertical slice done, one
red→green cycle — `SlotClosing` state.

Seam wrinkle surfaced before writing the test and confirmed with the
user: `segmentImpl.Close()` holds `s.mu` locked for its *entire* body,
including the disk write it waits on, and the existing `index()` getter
also takes `s.mu`. A `closing()` getter built the same way would just
block until `Close()` finished, then report `SlotClosing` for zero
observable time — never actually visible to a live status query. Fix:
`poolSlot` gained `closing() bool`, backed on `segmentImpl` by a
lock-free `closingFlag atomic.Bool`, set `true` as the very first thing
in `Close()` — before `s.mu.Lock()` — so it stays readable without
contention for the whole close. `Pool.Slots()` now reports `SlotClosing`
for the FIFO head when `closing()` is true, overriding `SlotActive`
(non-head/queued slots can't be `SlotClosing` — only the head is ever
actively writing/closing).

2026-08-17, `/mattpocock-skills:tdd`: third slice done — section-box byte
sizes (design points 3/4), plus two concurrency corrections surfaced and
fixed along the way. Several red→green cycles:

- `toc.EncodedSize(n uint32) uint32` (new exported function in package
  `toc`, `toc/columns.go`): the closed-form TOC-size formula, exposed
  outside the package for the first time (previously only the unexported
  `columnOffsets` computed this, tested in-package). Verified against
  hand-computed literals (n=0→128, n=1→512, n=100→3072), independent of
  `columnOffsets` itself, in `toc/columns_test.go`.
- Seam confirmed with the user: `Pool.Slots(defaults SectionSizes)
  []SlotStatus` — prolog/catalog/epilog are Storage-wide constants (same
  for every fblock at a given moment), not per-slot state, so the caller
  (Unit, which owns geometry/params) computes them fresh each call and
  Pool denormalizes them into every row, free rows included (matching
  design point 1's "same visual structure" requirement) — no Pool→Unit
  back-reference.
- **Bug found and fixed** (from the *previous* slice, slice 2): `index()`
  still took `s.mu`, and `Pool.Slots()` called it *before* checking
  `closing()` — so a live status query blocked for `Close()`'s entire
  duration regardless of the lock-free `closing()` fix, defeating that
  slice's whole point. Fixed by mirroring `s.idx`/`s.promoted` into
  lock-free `idxAtomic atomic.Uint32`/`promotedAtomic atomic.Bool`,
  updated at every assignment site; `index()` now reads only those.
  Covered by `TestSegmentImpl_IndexNonBlockingWhileMuHeld`
  (`segment_test.go`): holds `impl.mu` manually (no real Close() needed,
  avoids I/O-timing flakiness) and asserts `index()` returns within
  100ms.
- **Second bug found and fixed while adding `contentBytes()`**: the
  atomic publication above was itself placed too early in
  `promoteLocked` — *before* `s.handle`/`s.headerLen` were set. A
  lock-free reader observing `promotedAtomic==true` in that window would
  see a not-yet-assigned `s.handle`. Fixed by moving the
  `idxAtomic`/`promotedAtomic` stores to the very end of `promoteLocked`,
  after `s.handle` is fully assigned — the standard safe-publication
  ordering (write fields, then flip the flag; check the flag, then read
  the fields).
- `poolSlot` gained `contentBytes() int64` (flushed `WriteHandle.Written()
  - headerLen` once promoted, else a new lock-free `backlogLenAtomic`
  mirroring `len(s.backlog)`) and `elementCount() int` (`Filler.Len()`,
  already lock-free via its own internal mutex, independent of `s.mu`).
- `SlotStatus` gained `PrologSize`/`CatalogSize`/`ContentSize`/`TOCSize`/
  `EpilogSize`; `Pool.Slots()` fills `ContentSize`/`TOCSize` live per
  occupied slot (`TOCSize` via `toc.EncodedSize(elementCount())`, never
  `toc.Build`), and prolog/catalog/epilog from `defaults` uniformly
  (every row, including free — free rows get `TOCSize =
  toc.EncodedSize(0) = 128`, the empty-container default).

2026-08-17, `/mattpocock-skills:tdd`: fourth slice done — the live
transport (design point 5 / spec.md item 13). Seams confirmed with the
user first:

- Transport model: periodic ~500ms server push (not event-driven) — the
  only way `ContentSize`/`TOCSize` actually track a segment's continuous
  growth between Pool's own discrete reserve/promote/release
  transitions, matching design point 3's "content grows as frames are
  appended".

Two pieces, each its own red→green cycle:

- `internal/storage`: `Unit.PoolSlots() ([]SlotStatus, error)` — the
  missing link between `Pool.Slots(defaults)` (which needs
  `SectionSizes` but has no way to compute them, by design, no
  Pool→Unit back-reference) and a caller. Encodes a throwaway
  `*fblock.Header` (`Params: u.currentParams()`, `Catalog:
  u.mgr.Snapshot()`) purely to read back `Prolog.ParamsSize`/
  `CatalogSize` — exactly what `promoteLocked` itself does when actually
  promoting a segment, just without the write. Tested end-to-end via the
  package's real `initAndOpen`/`BeginSegment` integration-test style
  (`segment_test.go`), not `fakePoolSlot` (this method has no interface
  to fake — it's gluing two already-tested real pieces together).
- `internal/api` (`eventpush.go`): `subscribeMessage` gained
  `IncludePool bool` (off by default, mirroring `IncludeTOC`'s existing
  convention exactly — including a `TestEventPushServer_
  PerStorageWithoutIncludePool` regression guard, mirroring
  `..._PerStorageWithoutIncludeTOC`). A per-storage subscriber that sets
  it gets an immediate `poolPushMessage` (`{"type":"pool",...}`, one
  `poolSlotMessage` row per slot, `State` as a wire string via
  `poolSlotStateNames`) on connect, then every `poolPollInterval` (500ms,
  the same constant value as `fblocktree.go`'s `liveTreePollInterval`,
  same precedent) — implemented as a `var tick <-chan time.Time` left
  nil (blocks forever in `select`) unless `IncludePool`, added as one
  more `case` in `ServeHTTP`'s existing per-storage event loop, not a
  separate endpoint (per spec.md item 13's "reuses the existing feed").

2026-08-17, `/mattpocock-skills:tdd`: fifth and final slice done — the
`web/` component. Seam confirmed with the user first:

- Bar scale: each of the 5 section boxes is sized proportional to the
  Storage's `FblockSize` (already available client-side via
  `StorageInfo.geometry.FblockSize`, no backend change needed), not to
  the sum of the 5 known sizes — so unused fblock capacity shows as a
  real, visible remainder in the bar. Matches the ticket's own framing
  ("live pool-*fullness*-status list").

Three red→green cycles:

- `web/src/api/pool.ts`: `PoolSlot`/`PoolPushMessage` types mirroring
  `internal/api/eventpush.go`'s `poolSlotMessage`/`poolPushMessage`
  verbatim (snake_case field names — `prolog_size` etc. — same convention
  as `CatalogEntry`/`TreeNode` elsewhere in `api/`).
- `web/src/components/PoolStatusList.tsx`: pure presentational component,
  `{ slots, fblockSize }` props — one `.pool-status-row` per slot, a
  `.pool-slot-square` (CSS class keyed by `state`) plus a
  `.pool-section-bar` of 5 proportionally-widthed `.pool-section-*` spans
  (`width: (bytes/fblockSize)*100%`). Tested via RTL against
  hand-computed literal percentages (100/1000→10%, 128/1000→12.8%, etc.),
  independent of the component's own division.
- Wiring: `subscribeStorageEvents` (`api/events.ts`) gained a 5th
  optional `PoolOptions` param (`{ includePool, onPool }`) — routes
  `type: "pool"` frames to `onPool` instead of `onEvent` on the *same*
  connection/subscribe message (mirrors farcd's own
  `subscribeMessage.IncludePool`, avoids opening a second WS to the same
  storage). First-ever test for this file (`events.test.ts`) — a small
  `FakeWebSocket` test double, since jsdom's own `WebSocket` can't
  actually connect and none existed yet. `FblocksGridPage` now tracks
  `poolSlots` state, passes `{ includePool: true, onPool: setPoolSlots }`,
  and renders `<PoolStatusList>` above the existing squares grid, scaled
  to the selected storage's `geometry.FblockSize` (looked up from the
  already-fetched `storages` list).

Full repo green: `go build`/`go vet`/`go test ./...` (Go side), `npx
vitest run` (26 tests, web side), `npx tsc --noEmit`, and `npm run build`
(production Vite build, including the new component/wiring) all pass.
Not independently verified in a live browser against a running farcd —
no real backend was spun up for this session; the component/wiring
behavior is covered by the tests above, but actual visual appearance
(colors, bar proportions in a real browser) hasn't been eyeballed.

This closes every item from "What implementing this would actually
require" at the top of this file — `internal/storage`, `internal/api`,
and `web/` are all done. The pool-status-list feature is now fully
implemented end to end.

## Follow-up (2026-08-17, via `/mattpocock-skills:grilling` +
`/mattpocock-skills:tdd`): bidirectional fill + content color

Bug report that triggered this: with a 64MB fblock and 5×2Mbit channels,
the user expected the bar to fill "from both sides" (content
left-to-right, TOC right-to-left) and found the fill "unreasonably
slow". Grilling settled two separate things:

- **Redesign** (implemented this cycle): `PoolStatusList.tsx`'s
  `.pool-section-bar` now holds two flex groups —
  `.pool-section-left` (prolog, catalog, content) and
  `.pool-section-right` (toc, epilog) — in a `justify-content:
  space-between` row, so content grows left-to-right and toc grows
  right-to-left, meeting in a shrinking free-space gap that mirrors the
  real on-disk section layout. One red→green cycle: a structural test
  asserting `.pool-section-left`'s/`.pool-section-right`'s children and
  their order (existing width-percentage tests were untouched by
  `querySelector`'s depth-independence). `.pool-section-content`'s color
  changed from green (`#198754`) to blue (`#0d6efd`), matching
  `.fblock-square.state-in_progress`'s existing blue — a plain CSS edit,
  not test-driven (same as the other 4 section colors).
- **Deferred, not fixed**: investigation (background fact-finding, no
  code read live) found the real content-flush cadence is governed by
  ADR-017 — a fchunk-size OR flush-timeout (default 5s) trigger,
  whichever fires first (`internal/storageengine/engine.go:303-316`).
  With the web form's default 4 MiB `FchunkSize` and 10Mbit aggregate
  (~1.25MB/s), one fchunk fills in ~3.36s, so the on-disk `ContentSize`
  genuinely advances in ~6.25%-of-block jumps every ~3.3-5s, not
  smoothly — while the TOC size estimate (`toc.EncodedSize(elementCount)`
  in `pool.go`, fed by `Filler.Len()`) updates in-memory on every
  `AddFrames` call, many times a second, independent of any disk flush.
  So even with this redesign, content will visibly "step" while toc
  grows smoothly. The user explicitly deferred judging whether this is a
  real bug until after seeing the new visualization live — no change was
  made to `segment.go`'s flush cadence, `pool.go`'s size computation, or
  `eventpush.go`'s poll interval in this cycle.

Full web suite green: `npx vitest run` (27 tests), `npx tsc --noEmit`,
`npm run build`. No Go changes this cycle. Not verified in a live
browser against a running farcd — same caveat as the initial
implementation above.
