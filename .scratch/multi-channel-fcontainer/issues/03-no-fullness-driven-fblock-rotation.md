# Continuous recording never closes/rotates a fblock by fullness — overflows into the next fblock's disk space

Status: fixed, unit-tested, and verified end-to-end against the real
mediamtx/O_DIRECT e2e stack (2026-08-18) — `e2e/tests/continuous-rotation.spec.ts`
now passes; was blocked on `.scratch/fblocks-ui/issues/
10-header-pad-content-offset-mismatch.md`, now resolved (see that ticket's
own "## Fix" section)

## Discovery

2026-08-17, during a `/mattpocock-skills:grilling` session about a
different bug (`.scratch/fblocks-ui/issues/09-pool-bar-static-on-fresh-deploy.md`),
the user asked for an e2e test that watches a continuous, never-stopped
recording until "the fblock-completion event" (`fblock.ready`) fires. That
raised the question of what actually triggers a continuous shared
segment's `Close()` in the first place, since `internal/ingest` never
calls it (`StorageSegment`'s own doc comment: "the pool rotates purely by
fullness, independent of any one channel").

Reproduced empirically on a clean e2e stack (`e2e/docker-compose.e2e.yaml`,
mediamtx + `sample.mp4`, no real camera involved): created a Storage with
`FblockSize=8MiB, N=4` (file pre-allocated to 32MiB total via
`CreateSizedFile`), one `continuous` channel, started recording, **never
called `recording/stop`**. Polled the pool-status WS
(`GET /events/ws` with `include_pool`):

```
fblock 0 content_size: 32124928 bytes (~30.6 MiB) -- still growing
```

— content_size for a single fblock exceeded its own 8 MiB `FblockSize` by
~4x, still climbing. The underlying `.img` file itself had physically grown
from its originally-allocated 32 MiB to 44 MiB (`ls -la` on the volume).
Fblock 0 never left `in_progress`; fblocks 1-3 stayed `uninitialized` the
whole time — meaning fblock 0's writes were physically landing in what
should have been fblock 1/2/3's on-disk region (or extending the file past
its allocation, depending on how far past N×FblockSize it got), silently
overwriting/corrupting them.

## Root cause

`.scratch/multi-channel-fcontainer/issues/02-ingest-shared-filler-per-storage.md`'s
"Close dynamics" section is an explicit, already-settled design decision:

> Шhe shared Filler's lifecycle (when it closes and a new one opens) is
> driven **purely by byte-fullness** (ticket 01's mechanism), never by any
> individual channel's start/stop.

...crediting `01-storage-buffer-pool-and-early-index-assignment.md`
("ticket 01") as the ticket that implements this fullness check. It
doesn't. Ticket 01's "Comments" section lists everything it actually built
— `internal/storage/pool.go` (`Pool`/`PoolStatus`/`PoolTuning`),
`segment.go`'s magic-trailer promotion/close/retry, `storageengine`'s
`EnqueueOpenWrite`/periodic flush, crash recovery — none of it is a
size-triggered `Close()`. Confirmed by direct code search: nowhere in
`internal/storage` or `internal/ingest` does any code path compare
accumulated content against `fblock.ContentSize(...)`/`u.contentCapacityEstimate()`
*before* accepting more `Append`/`AddFrames` calls — the only such
comparison is `segment.go`'s `writeTailLocked` (`content %d bytes exceeds
capacity %d`), and that only runs *inside* `Close()`, which for a
continuous channel is only ever invoked by an explicit `recording/stop` or
(since issue 08) `failLocked`'s write-failure path. Nothing calls it on
reaching fullness.

`docs/docs/archive/10-capture-policy.md` also states this as settled fact
("его открытие/закрытие определяется исключительно заполненностью
(ADR-017, `internal/storage/pool.go`)") — but ADR-017 itself, read in
full, only specifies the periodic *partial flush* trigger (write early,
don't wait for close); it says nothing about closing on fullness either.
Both docs point to a decision that was made and recorded twice, but never
implemented.

## Impact

Any `continuous`-policy channel (the primary intended mode for 24/7
archival, per the whole point of `write_mode: cyclic`) that runs long
enough without an explicit `recording/stop` call will silently write past
its fblock's capacity into the next physical fblock(s)' disk region,
corrupting them, and/or grow the backing file unbounded on a regular-file
backend. This is not a rare edge case — it is the *expected, unavoidable*
outcome of leaving continuous recording running, which is the entire
point of continuous recording. Given ADR-017's periodic flush (issue 08)
now reliably gets bytes to disk quickly, this failure mode is reachable
in normal operation within one `FblockSize`'s worth of recording time,
not just under some pathological condition.

Distinct from `.scratch/fblocks-ui/issues/09-pool-bar-static-on-fresh-deploy.md`
(the periodic-flush timer sometimes not firing at all) — that's "writes
stop happening"; this is "writes never stop happening, and go somewhere
they shouldn't."

## Design (settled via `/mattpocock-skills:grilling`, 2026-08-17)

- **Mechanism.** When accumulated content reaches the live capacity
  estimate, `segmentImpl` closes itself (real TOC/epilog, promote to
  `Ready` — a *successful* close, not `failLocked`'s `Bad`) and returns
  `ErrSegmentClosed` to the caller. This reuses the *already-existing*
  `ErrSegmentClosed` retry in `internal/ingest/storagesegment.go`'s
  `StorageSegment.call` (the same mechanism issue 08's `failLocked`
  already relies on) — no new retry plumbing needed on the ingest side.
- **Capacity threshold: live `toc.EncodedSize` estimate.** On each call,
  recompute the real current capacity as
  `fblock.ContentSize(FblockSize, paramsSize, catalogSize,
  toc.EncodedSize(uint32(s.filler.Len())))` — the exact same estimate
  `pool.go`'s `Slots()` already computes for the pool-status-list bar's
  own `TOCSize` display (`toc.EncodedSize` is exact given a row count, not
  approximate, since TOC is a fixed-width-per-row SoA format) — rather
  than a fixed margin off the toc_size=0 upper bound.
- **Check site: proactive, in `AddFrames`/`AddStreamParams`, before
  touching `s.filler`.** `Filler` is append-only with no rollback, so the
  check can only compare content size *as of the previous call* against
  the threshold — not this incoming batch's own (not-yet-known) encoded
  size. If already over threshold: reject the whole batch, don't touch
  `s.filler`, close and return `ErrSegmentClosed`. If not yet over: accept
  the whole batch into `s.filler` unconditionally, even if this specific
  batch pushes size past the threshold — that overshoot is bounded to one
  caller's batch (small: one `AddFrames`/`AddStreamParams` call) and gets
  caught on the *next* call. Accepted as an unavoidable consequence of
  `Filler`'s no-rollback API, the same kind of bounded imprecision ADR-017
  already accepts for low-bitrate channels' flush timing.
- **Refactor `Close()` into `closeLocked`.** `Close(now)` takes `s.mu`
  itself, but the new check lives inside `AddFrames`/`AddStreamParams`,
  which already hold `s.mu` — a direct `s.Close(now)` call there would
  self-deadlock. Extract `Close()`'s locked body into a private
  `closeLocked(now)` (assumes the lock already held, same pattern as
  `promoteLocked`/`registerChannelLocked`), with `Close()` reduced to
  `s.mu.Lock(); defer Unlock(); return s.closeLocked(now)`. The new
  fullness branch calls `closeLocked` directly. Lock ordering is
  unchanged from today's `Close()` (`s.mu` then, inside, `pool.mu` via
  `u.pool.release`) since it's the identical code, just invoked from a
  second call site under the same lock.
- **Applies uniformly to continuous and event channels** — falls out for
  free from living in the shared `segmentImpl` code, not any one
  `CapturePolicy`'s per-policy-type logic.
- **Tests: both.** A unit test in `internal/storage/segment_test.go`
  mirroring issue 08's `TestSegment_WriteFailureMarksFblockBadAndOpensFreshSegment`
  shape — tiny `FblockSize`, drive `AddFrames` past the live capacity
  estimate, assert `ErrSegmentClosed` + fblock state `Ready` (not `Bad`) +
  a fresh segment opens at a new index — plus a Playwright e2e case
  against the real `mediamtx` stack (this grilling session's original
  ask): a small-`FblockSize` Storage, one continuous channel never
  stopped, watch the pool-status WS / journal feed until `fblock.ready`
  fires on its own, then confirm playback via hls.js.

## Fix (2026-08-18)

Implemented exactly per the design above:

- `internal/storage/segment.go`: `Close(now)` refactored into a thin
  wrapper (`closingFlag`, lock, `closed` check/set) over a new
  `closeLocked(now)` doing the actual work — no behavior change, verified
  by the full existing test suite passing unchanged before any new
  behavior was added.
- New `segmentImpl.contentLen int64` field, incremented inside
  `pushReadyLocked` by every incremental tail's encoded length — the
  actual source of truth for "how much content is really in this
  segment," independent of the async periodic-flush engine goroutine
  (which `contentBytes()`/`WriteHandle.Written()` lag behind, discovered
  the hard way: an early version of `isFullLocked` used `contentBytes()`
  and let content grow ~40% past real capacity before ever triggering,
  because it was reading stale disk-flushed-so-far state instead of the
  true in-memory total).
- New `segmentImpl.isFullLocked()`: proactive check at the top of
  `AddFrames`/`AddStreamParams` (before touching `s.filler`), comparing
  `contentLen` against a live `fblock.ContentSize(FblockSize, paramsSize,
  catalogSize, toc.EncodedSize(filler.Len()))` estimate, reserving a
  **margin of one `fchunk_size`** below that estimate. The margin was not
  in the original design — discovered necessary during implementation:
  `Filler` has no rollback, so a batch that's already been let through
  is irrevocably part of the segment's content by the time the *next*
  call notices the threshold was crossed. Without a margin, one large
  batch (e.g. a prerecord-replay burst) could push real content past the
  fblock's actual physical capacity, hitting `writeTailLocked`'s own
  pre-existing hard "content exceeds capacity" guard instead of a clean
  close. Reusing `fchunk_size` (already a configured, meaningful "unit of
  write work" for this Storage) avoided inventing a new margin constant.
- New `segmentImpl.closeForFullnessLocked(now)`: sets `s.closed = true`,
  calls `closeLocked(now)`, and always returns `ErrSegmentClosed` on
  success (propagating any real error instead) — deliberately not
  `failLocked`'s shape, since reaching capacity is a *successful* close
  (fblock ends up `Ready`), not a failure (`Bad`).
- Test: `TestSegment_FullnessClosesFblockAsReadyAndOpensFreshSegment`
  (`internal/storage/segment_test.go`), mirroring issue 08's own
  write-failure test shape. Green, including full `go test ./...` and
  `-race` on `internal/storage`/`internal/storageengine`/`internal/ingest`
  with no regressions.

### e2e verification found and partially fixed a separate, deeper bug

Verifying this end to end required a continuous segment to actually reach
`Ready` against the real default O_DIRECT backend for the first time ever
in this codebase's history (nothing before this ticket ever drove a
periodic-flush segment through `Close` against real O_DIRECT — see issue
10 for why). Doing so surfaced two problems in `writeTailLocked`'s tail
write, both **pre-existing latent bugs in issue 08's original fix, not
regressions from this ticket's own change**:

1. Wrong write offset (`WriteHandle.Written()` excludes `headerPadLen`,
   but the physical write position doesn't) — **fixed** as part of this
   ticket: new `WriteHandle.TrailerOffset()`
   (`internal/storageengine/engine.go`), used in `writeTailLocked` instead
   of `Written()`. Covered by a new regression test scaffold,
   `alignmentEnforcingBackend` (`internal/storage/integration_test.go`) —
   a test-only backend wrapper that rejects misaligned `WriteAt` calls
   without needing a real O_DIRECT-capable filesystem, exactly the gap
   issue 08 itself flagged ("every existing test uses `OpenStandard`,
   `Alignment()==1`, none exercise real alignment").
2. A deeper format bug — `fblock.ComputeOffsets`'s `ContentOffset` doesn't
   account for `headerPadLen` at all, so any fblock actually completed via
   the periodic-flush path with real padding applied would be **read back
   at the wrong offset** (silent corruption, never previously observed
   because nothing had ever reached `Ready` this way before). Filed
   separately as `.scratch/fblocks-ui/issues/
   10-header-pad-content-offset-mismatch.md` since it needed its own
   grilling round (touched `fblock.ComputeOffsets`'s public signature and
   every reader that calls it) — **now fixed**, see that ticket's own "##
   Fix" section. This ticket's own
   `TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend` unit test and
   `e2e/tests/continuous-rotation.spec.ts` are both un-skipped and green as
   part of issue 10's fix.

Also fixed in passing (blocked the e2e stack entirely, not specific to
this ticket): `e2e/docker-compose.e2e.yaml`'s `hls_server` service was
missing `HLS_SERVER_CACHE_DIR`, which `internal/hlsconfig` requires
regardless of `cache_backend` — without it `hls_server` (and, cascading
from its failed DNS resolution at nginx boot, `web`) crash-looped
unconditionally, meaning **no** e2e Playwright test in this repo (not just
the new one) could have passed on this stack as it stood. Added the same
`/var/cache/hls_server` value the main `docker-compose.yaml` already uses.
