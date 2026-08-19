# Give the fblock write transaction to `Unit`, instead of re-assembling it at every call site

Status: fixed (2026-08-19, via `/mattpocock-skills:improve-codebase-architecture` + `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

`internal/storage/segment.go` hand-assembles the fblock write transaction
identically at multiple call sites instead of it living behind one
interface:

- **begin-write** (`SelectNextIndex` → `Snapshot` (for `wasReady`/
  `prevUUID`) → `BeginWrite` → publish `EventFblockWriteStarted` +
  conditional `EventFblockDeleted` → `SetChannelBit` per channel → a
  second `Snapshot`, hand-patched with `UUID`/`Begin`/`End` → `
  nextWriteSequence` → `saveSSDCatalogBestEffort`) — duplicated in
  `promoteLocked` (lines ~191-253) and the retry loop inside `closeLocked`
  (lines ~547-598).
- **complete-write** (`CompleteWrite` → `saveSSDCatalogBestEffort` →
  publish `EventFblockWriteCompleted` → `health.RecordWrite(false)` →
  `health.CheckBadRatio`) — duplicated in `writeTailLocked`'s success path
  (lines ~688-695) and the retry loop's success path in `closeLocked`
  (lines ~616-623).
- **fail-write** (`health.RecordWrite(true)` → publish
  `EventFblockWriteFailed` → `MarkBad`) — duplicated byte-for-byte across
  four sites: `failLocked`, the pre-retry-loop corrupted branch in
  `closeLocked`, the in-loop corrupted branch in `closeLocked`, and
  `writeTailLocked`'s corrupted branch.

This is why `segment.go` keeps coming back as a hotspot across recent
history (5-6 changes in the last ~150 commits per `git log`): a bug in the
transaction — e.g. forgetting `EventFblockDeleted`, which msm_server's
`fblocks_del` reporting depends on (see `CONTEXT.md`'s `fblocks_add /
fblocks_del` entry) — has to be fixed at up to four sites, not one.
`internal/storage/consistency.go`'s crash-recovery logic explicitly
depends on the hand-patched snapshot behavior being correct at every write
site (its own comment: "both Startup paths load a catalog where the
in-flight fblock's own entry was already patched with its real identity
before being written").

## Why not `index.Manager`

The obvious first instinct — move this into `internal/index.Manager` as
`BeginFblockWrite`/`CompleteFblockWrite`/`FailFblockWrite` — was rejected
during grilling. `Manager` is deliberately kept a pure, side-effect-free
domain object today (its own package doc: "this package never calls
`time.Now()` itself, for testability"); it has no dependency on
`NotificationBus`, `HealthMonitor`, or the SSD catalog, all of which are
`internal/storage`'s own types owned by `Unit`. Giving `Manager` those
dependencies just to host this transaction would break that intentional
purity and reverse the package's current import direction.

`Unit`'s own doc comment already states the preferred shape instead:
"Recorder/Reader (recorder.go, reader.go) are methods on `Unit` rather than
separate types — every operation they need... already lives here... so
splitting them out would only add constructors that thread the same shared
state through twice." The write transaction is the same situation:
`Unit` already holds `mgr`, `notify`, `health`, and `engine` — everything
the transaction needs.

## Design (settled via grilling, 2026-08-19)

Three new unexported methods on `Unit` (package `internal/storage`; file
placement — `unit.go` or a new `writetxn.go`, implementer's call).
`internal/index.Manager` is **not modified at all**.

- `beginFblockWrite(now uint64, uuid [16]byte, positions map[uint16]uint16, begin, end uint64) (idx uint32, h *fblock.Header, err error)`
  — hides the whole begin-write sequence described above. Returns a fully
  populated `*fblock.Header` (`Prolog` with `WriteSequence`/`CatalogTime`
  set, `Params`, `Catalog` = the patched snapshot) so each caller only
  picks which `assemble*` variant to use and which `engine.Enqueue*` to
  call — the two current callers diverge exactly there:
  `promoteLocked` wants `assembleHeaderAndMagic` + `EnqueueOpenWrite` (no
  content yet), the `closeLocked` retry loop wants `assembleFblock` (with
  content + TOC) + `EnqueueWrite`. Returning raw components instead
  (`idx, seq, snap, params`) was considered and rejected — it just moves
  the `fblock.Header` assembly duplication from one place to two.
- `completeFblockWrite(idx uint32, uuid [16]byte, begin, end, seq, now uint64) error`
  — `CompleteWrite` → `saveSSDCatalogBestEffort` → publish
  `EventFblockWriteCompleted` → `health.RecordWrite(false)` →
  `health.CheckBadRatio`. Deliberately excludes `pool.release` — that
  needs the `*segmentImpl` itself as a `poolSlot`, which this method has
  no business knowing about.
- `failFblockWrite(idx uint32, uuid [16]byte) error` — `health.RecordWrite(true)`
  → publish `EventFblockWriteFailed` → `MarkBad`. Also excludes
  `pool.release`/`s.closed = true`, left to each of the four call sites
  (`failLocked` needs `pool.release` afterward and returns
  `ErrSegmentClosed`; the `closeLocked` corrupted branches just `continue`
  the retry loop or fall through).

No concurrency concerns beyond what already exists: `Manager` guards its
own state with its own mutex regardless of caller; `promoteLocked` is only
ever invoked by `Pool` while holding `Pool`'s own lock, and `closeLocked`'s
retry only ever runs for the currently-promoted segment.

### Call sites after the refactor

- `promoteLocked`: `beginFblockWrite` → `assembleHeaderAndMagic(h, ...)` →
  `engine.EnqueueOpenWrite(...)` → set `s.idx`/`s.promoted`/`s.headerLen`/
  `s.paramsSize`/`s.catalogSize`/`s.writeSeq` from `idx`/`h`.
- `closeLocked`'s retry loop: `beginFblockWrite` → `assembleFblock(h,
  contentBuf, tocBuf, ...)` → `engine.EnqueueWrite(...)` → on corrupted,
  `failFblockWrite` + `continue`; on success, `completeFblockWrite` +
  `pool.release`.
- `writeTailLocked`: on corrupted, `failFblockWrite`, return `false, nil`
  (unchanged fallback-to-retry behavior); on success, `completeFblockWrite`
  + `pool.release`.
- `failLocked`: `failFblockWrite` + `pool.release` + `s.closed = true` +
  return `ErrSegmentClosed` (unchanged).

## Tests

- All existing `internal/storage/segment_test.go` and `integration_test.go`
  tests must keep passing unchanged — they already exercise this
  transaction end-to-end via `initAndOpen`/real `segmentImpl` and serve as
  regression coverage; behavior does not change, only where the code lives.
- Two new targeted tests on `Unit` directly, made cheap by the new small
  interface (no `segmentImpl`/`Filler`/TOC setup needed): neither branch is
  currently asserted by any existing test.
  1. `beginFblockWrite` publishes `EventFblockDeleted` when the selected
     index was previously `Ready` (subscribe to the `NotificationBus`, call
     `beginFblockWrite` twice against a small geometry so the second call
     reuses a `Ready` slot, assert the event fires with the right
     `UUID`).
  2. `beginFblockWrite` publishes `EventStorageAlert`/`AlertNoFreeFblocks`
     when `SelectNextIndex` is exhausted (fill every slot `Ready` and
     within retention, call `beginFblockWrite`, assert the alert event and
     the returned error).

## Fix (2026-08-19)

Implemented exactly per the design above, via TDD (`/mattpocock-skills:tdd`):

- New `internal/storage/writetxn.go`: `Unit.beginFblockWrite`,
  `Unit.completeFblockWrite`, `Unit.failFblockWrite` — signatures and
  behavior exactly as designed. `internal/index` untouched.
- New `internal/storage/writetxn_test.go`, 5 tests written test-first
  (red before each implementation): the two previously-uncovered event
  branches from the design doc (`TestUnit_BeginFblockWrite_PublishesFblockDeletedWhenReusingReadySlot`,
  `TestUnit_BeginFblockWrite_PublishesStorageAlertWhenNoFreeFblocks`), plus
  a happy-path test for `beginFblockWrite` and one each for
  `completeFblockWrite`/`failFblockWrite` — all drive `Unit` directly, no
  `segmentImpl`/`Filler`/TOC setup needed, confirming the new seam really
  is cheaper to test than the old shape.
- `internal/storage/segment.go`: `promoteLocked`, `closeLocked` (both the
  pre-retry corrupted branch and the retry loop itself), `writeTailLocked`,
  and `failLocked` all rewired to call the three new methods instead of
  re-assembling the transaction inline. Behavior preserved exactly,
  including the one asymmetry worth flagging: a hard I/O error from
  `ticket.Wait()` (`werr`/`err != nil`) only calls `health.RecordWrite(true)`
  and returns — it does **not** go through `failFblockWrite` (no
  `MarkBad`/no `fblock.write.failed` event), unlike a corrupted
  write-verify result. This distinction already existed in the
  pre-refactor code at all three sites and was carried over unchanged, not
  introduced by this ticket.
- No new lint debt: `golangci-lint run ./internal/storage/...` shows the
  same 3 pre-existing `noinlineerr` violations as `HEAD` (verified via a
  throwaway worktree), none of them in the new code — the new methods and
  every rewired call site use the two-line `err = ...; if err != nil`
  form instead of `if err := ...; err != nil`.
- Full `go test ./...` and `go test -race ./internal/storage/...
  ./internal/ingest/... ./internal/farcd/...` green, no regressions.
  `gofmt -l` clean.

## Comments
