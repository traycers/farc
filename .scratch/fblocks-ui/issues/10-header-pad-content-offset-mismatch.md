# issue 08's headerPadLen shifts real on-disk content, but readers never account for it — corrupts any fblock completed via periodic-flush on a real O_DIRECT backend

Status: fixed, unit-tested, and verified end-to-end against the real
mediamtx/O_DIRECT e2e stack (2026-08-18)

## Discovery

2026-08-18, while verifying `.scratch/multi-channel-fcontainer/issues/
03-no-fullness-driven-fblock-rotation.md`'s fix end to end (a new e2e
Playwright test, `e2e/tests/continuous-rotation.spec.ts`, against the real
mediamtx+O_DIRECT stack). That test needs a continuous segment to actually
reach `Ready` on its own — the first time this codebase has ever driven a
periodic-flush segment (`internal/storageengine`'s `EnqueueOpenWrite`) all
the way through `Close()`/`writeTailLocked` against a **real** O_DIRECT
backend (every existing unit test uses `ioengine.OpenStandard`,
`Alignment()==1`, where `headerPadLen` is always 0 and this bug is
invisible).

The e2e run failed with `ioengine: offset/length not aligned to required
block size` inside `writeTailLocked`. Two distinct problems were found
while chasing it:

1. **Fixed as part of issue 03's own work**: `writeTailLocked` positioned
   its tail write using `WriteHandle.Written()`, which (per issue 08's own
   design) deliberately *excludes* `headerPadLen`. Since the *physical*
   write position on disk includes that padding, this under-counted the
   true offset by exactly `headerPadLen` bytes. Fixed by adding
   `WriteHandle.TrailerOffset()` (returns `job.pos - job.trailerLen`,
   INCLUDING `headerPadLen`) and using it instead — see
   `internal/storageengine/engine.go` and
   `internal/storage/segment.go:writeTailLocked`. Covered by a new
   regression test, `TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend`
   (`internal/storage/segment_test.go`), using a new
   `alignmentEnforcingBackend` test helper (`internal/storage/
   integration_test.go`) that rejects misaligned `WriteAt` calls without
   needing a real O_DIRECT-capable filesystem.

2. **This ticket, NOT fixed**: fixing (1) alone still leaves the tail
   write's *length* misaligned, because the deeper problem is structural:
   `fblock.ComputeOffsets(paramsSize, catalogSize)` (`fblock/geometry.go`)
   computes `ContentOffset` as `FixedPrologSize + paramsSize + 8 +
   catalogSize + HeaderChecksumsSize + 8` — i.e. immediately after
   `magic_content`, with **no knowledge of `headerPadLen` at all**. Every
   reader that locates where content starts (`internal/storage/reader.go`,
   `consistency.go`, anything built on `ComputeOffsets`) uses this same
   unpadded value. But the **writer** (`internal/storageengine.
   EnqueueOpenWrite`, issue 08) physically inserts `headerPadLen` zero
   bytes *between* `magic_content` and the first real content byte, on any
   backend where `Alignment() > 1` and the header doesn't happen to land
   on an alignment boundary (i.e. essentially always, for a real
   deployment's default O_DIRECT backend). `headerPadLen` is never
   persisted anywhere in the on-disk format (prolog/params/catalog/
   checksums) or derivable from it — it only ever exists transiently in
   `storageengine.writeJob`, in memory, for the process instance that
   happened to write it.

   **Consequence**: any fblock that reaches `Ready` via the periodic-flush
   path (`EnqueueOpenWrite` + `Close`/`writeTailLocked`) on a backend with
   `Alignment() > 1` and `headerPadLen > 0` has its real content physically
   shifted forward by `headerPadLen` bytes relative to where every reader
   will look for it. Reading such a fblock back doesn't error — it just
   silently returns corrupted/misaligned data (the first `headerPadLen`
   bytes of "content" a reader sees are actually the tail end of the zero
   padding, and everything after is offset by that same amount).

   This has not been observed as user-visible corruption anywhere, because
   (confirmed across this investigation) **no continuous segment has ever
   successfully reached `Ready` against a real O_DIRECT backend before
   now** — issue 08's own e2e verification only checked that non-zero
   bytes appeared past `magic_content` while a segment was still
   `in_progress` (periodic flush working at all was the whole point of
   that ticket); it never drove a real O_DIRECT segment through to `Close`
   (nothing did, until issue 03's fullness-rotation made that possible for
   the first time). There is therefore no real backward-compatibility data
   at stake — every fix option below is free to choose whichever on-disk
   convention is simplest, without needing to stay compatible with
   anything already written this way in the wild.

## Why the one-shot write path (Init, WriteFcontainer's retry loop) is unaffected

`assembleFblock`/`Init` build one complete, already-`FblockSize`-byte
buffer and hand it to `EnqueueWrite` as a single write (offset 0, length
`FblockSize`, both inherently alignment-multiples per ADR-010) — there's
no incremental chunking boundary inside it that needs its own alignment,
so it has never needed (and doesn't have) any content-start padding.
`ComputeOffsets`'s unpadded `ContentOffset` is exactly correct for these
fblocks. The bug is specific to the periodic-flush (`EnqueueOpenWrite`)
path.

## Design (settled via `/mattpocock-skills:grilling`, 2026-08-18)

- **Strategy: alignment-aware `ContentOffset`, no format/persisted-byte
  change.** `fblock.ComputeOffsets(paramsSize, catalogSize uint32,
  alignment int) Offsets` gains an `alignment` parameter and rounds
  `ContentOffset` up to it (`MagicContentOffset` itself is unaffected —
  the gap sits between `MagicContentOffset+8` and the rounded
  `ContentOffset`). Both writers and readers pass `backend.Alignment()`
  from however they already have the backend in hand — every one of the 5
  existing call sites already does (`internal/storage/{headerio,reader,
  consistency}.go` all take `backend`/`*Unit` directly;
  `fblock/header.go`'s internal call, inside `DecodeHeader`, only uses the
  result for `ChecksumsOffset` — never `ContentOffset` — so it passes `1`
  unconditionally, unaffected either way). No format/ADR change: for a
  `standard` backend (`Alignment()==1`) every rounding is a no-op, so
  existing `standard`-backend behavior (all current tests, and any
  already-`Ready` fblock written that way) is byte-identical to today.
- **Uniform across both write paths.** The one-shot path
  (`assembleFblock`/`Init`/`Close`'s corruption-retry) now reserves the
  *same* gap the periodic-flush path (`EnqueueOpenWrite`) needs, even
  though geometrically it doesn't need one itself — so a reader never has
  to know or guess which path produced a given `Ready` fblock; one
  `ComputeOffsets(..., alignment)` formula is correct for all of them.
- **`fblock.ContentSize`/`MaxContainerSize`/`CheckMinContainerShare` also
  gain an `alignment` parameter**, subtracting the same gap from the
  content budget — required so `assembleFblock`'s now-larger buffer
  (header + gap + content + toc + epilog) still sums to exactly
  `FblockSize`, and so issue 03's `isFullLocked` live-capacity estimate
  and ADR-013's init-time share check both stay accurate. Every caller
  already has a backend in scope (`internal/storage/{assemble,consistency,
  init,segment}.go`).
- **Test**: extend `TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend`
  (currently `t.Skip`'d, `internal/storage/segment_test.go`) rather than
  adding a separate test — after `seg.Close()` succeeds, read the content
  back for real (`u.ReadTOC`/`u.ReadNodeValue`, the same pattern
  `TestSegment_CloseProducesReadableFcontainer` already uses) and assert
  the frame bytes round-trip correctly. A test that only checks `Close()`
  doesn't error would have missed this ticket's actual bug (a silent
  offset shift, not an error) — this is exactly why issue 03's own version
  of this test wasn't enough on its own. Un-skip once green; also re-enable
  `e2e/tests/continuous-rotation.spec.ts` (`test.skip` -> `test`).

## Rejected alternatives

- Persisting `headerPadLen` somewhere in the per-fblock header: a real
  format change needing an ADR revision, for no benefit over the
  deterministic-recomputation approach above (nothing needs a *value*
  persisted when every reader can derive the same gap from information it
  already has).
- Eliminating the padding's need entirely via a different periodic-flush
  strategy: revisits ADR-017 itself (already revised five times) for a
  narrower win than the chosen fix, which needs no ADR change at all.
- `internal/storageengine`'s own `WriteHandle.Written()`/`TrailerOffset()`
  semantics (issue 03's own fix) don't need to change further — the
  remaining gap was entirely on the `fblock`/`internal/storage` offset-
  computation side, not the write-scheduling side.

## Fix (2026-08-18)

Implemented exactly the settled design above, no deviations:

- `fblock.ComputeOffsets(paramsSize, catalogSize uint32, alignment int)
  Offsets` (`fblock/geometry.go`) now rounds `ContentOffset` up to
  `alignment` via a new `roundUp` helper; `MagicContentOffset` is
  unaffected. A new unexported `contentGap(paramsSize, catalogSize uint32,
  alignment int) int64` derives the reserved gap size from `ComputeOffsets`
  itself (single source of truth, no duplicated rounding arithmetic).
- `fblock.ContentSize`/`fblock.MaxContainerSize` both gained an `alignment`
  parameter and now subtract `contentGap(...)`. `fblock.CheckMinContainerShare`
  gained the same parameter, passed through to `MaxContainerSize`.
- All 5 real `ComputeOffsets` call sites updated: `fblock/header.go`'s
  `DecodeHeader` passes `1` (only ever reads `ChecksumsOffset`, documented
  inline as to why that's safe); `internal/storage/headerio.go`'s
  `readHeader`, `internal/storage/reader.go`'s `contentBaseOffset`, and both
  call sites in `internal/storage/consistency.go`
  (`recoverPartialWrite`/`verifyWriteCompletion`) all pass
  `backend.Alignment()`/`u.backend.Alignment()`. Every `ContentSize`/
  `MaxContainerSize`/`CheckMinContainerShare` call site (`internal/storage/
  {assemble,consistency,init,segment}.go`) updated the same way.
- **Uniform gap reservation across both write paths**: `assembleHeaderAndMagic`
  (`internal/storage/assemble.go`) now takes `alignment` and zero-pads its
  returned buffer up to `fblock.ComputeOffsets(..., alignment).ContentOffset`
  — the same gap `storageengine.EnqueueOpenWrite` inserts as `headerPadLen`
  for a periodic-flush job. `assembleFblock` takes `alignment` too, passing
  it through to both `assembleHeaderAndMagic` and `ContentSize`. Both
  `internal/storage/segment.go`'s `promoteLocked` (the periodic-flush path's
  `EnqueueOpenWrite` prefix) and its `closeLocked` corruption-retry loop (the
  one-shot `assembleFblock` path) now pass `u.backend.Alignment()`; so does
  `internal/storage/init.go`'s fblock-0 bootstrap write.
- **Side effect worth noting**: because `assembleHeaderAndMagic` now bakes
  the alignment gap into the buffer it hands `EnqueueOpenWrite`, that
  buffer's length is already a multiple of `alignment` by construction —
  `EnqueueOpenWrite`'s own `headerPadLen` computation (`internal/
  storageengine/engine.go`) naturally comes out to 0 in practice now (its
  `rem := len(data) % alignment` is always 0). `WriteHandle.Written()` and
  `WriteHandle.TrailerOffset()` are therefore equal in the common case
  today, but both are kept as-is: `TrailerOffset()` stays the physically
  correct value to use regardless, and nothing about `EnqueueOpenWrite`'s
  own contract needed to change (its `headerPadLen` field still exists to
  cover any future caller that hands it an unpadded prefix directly).
- **Tests**: `fblock/geometry_test.go` gained
  `TestComputeOffsetsRoundsContentOffsetUpToAlignment` and
  `TestContentSizeSubtractsAlignmentGap`; every other `fblock`/
  `internal/storage` test updated to pass `alignment=1` at its own
  `ioengine.OpenStandard`-backed call site (no behavior change there — a
  no-op rounding). `TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend`
  (`internal/storage/segment_test.go`) un-skipped and extended per the
  grilled test-seam decision: after `Close()`, reads the frame back via
  `u.ReadTOC`/`u.ReadNodeValue` and asserts the bytes round-trip correctly
  (not just that `Close()` didn't error) — green.
- **e2e**: `e2e/tests/continuous-rotation.spec.ts` un-skipped (`test.skip` ->
  `test`) and run against the real stack
  (`docker compose -f e2e/docker-compose.e2e.yaml up -d --build`, Playwright
  run from inside a container on the `e2e_default` network per this
  sandbox's proxy-interception workaround — see issue 03's own e2e notes):
  passes end-to-end — fblock 0 closes as `Ready` on its own (no
  `recording/stop` ever called), fblock 1 opens live for the still-recording
  channel, and the pre-rotation content plays back via hls.js. This is the
  first time a continuous segment has ever reached `Ready` against a real
  O_DIRECT backend in this codebase.
- Full `go test ./...` and `go test -race
  ./internal/storage/... ./internal/storageengine/... ./fblock/...` both
  green after the fix.
- **Not a regression, left untouched**: `e2e/tests/two-channel-playback.spec.ts`
  fails on a clean stand (`timed out waiting for confirmed candidates`) both
  before and independent of this fix — `internal/ingest/policy.go`'s
  `closeSegmentLocked` (git-diff-confirmed untouched this session, and by
  issue 03's own work) only stops that one channel's own contribution and,
  per its own doc comment, "never [closes] the shared segment" — a
  consequence of the pre-existing multi-channel-fcontainer shared-Filler
  architecture (`.scratch/multi-channel-fcontainer/issues/
  02-ingest-shared-filler-per-storage.md`), not of this ticket's fix. That
  test's own `setup.ts` already flags its "stop synchronously writes to
  disk" assumption as stale. Fixing it is out of scope here.

## Impact on other tickets

- `.scratch/multi-channel-fcontainer/issues/03-no-fullness-driven-fblock-
  rotation.md`'s own design/unit-test work is unaffected and correct on
  its own terms (its unit test uses `ioengine.OpenStandard`, which this
  bug can't reach) — only its **e2e verification** against the real
  mediamtx/O_DIRECT stack is blocked pending this ticket. Its e2e spec
  (`e2e/tests/continuous-rotation.spec.ts`) and the new
  `TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend` unit test are
  both left in the tree, marked skipped with a pointer to this ticket, to
  be re-enabled once this is fixed.
- `.scratch/fblocks-ui/issues/08-ingest-stalls-after-rtp-packet-loss.md`'s
  own "resolved" status is not reopened by this — its fix (padding exists
  at all, so writes stop failing outright) is correct and still necessary;
  this ticket is a follow-on gap in that fix's read-side consistency, not
  a regression of what it actually fixed.
