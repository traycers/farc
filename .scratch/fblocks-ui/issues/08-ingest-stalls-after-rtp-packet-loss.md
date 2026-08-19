# Continuous recording never persists on the default (O_DIRECT) backend — silently

Status: resolved (2026-08-17, via `/mattpocock-skills:grilling` +
`/mattpocock-skills:tdd`) — see Fix below

## Original symptom (misleading title, kept for history)

Discovered 2026-08-17 while investigating `issues/05` (which turned out to
be a false alarm — see that file's Comments). A channel's RTSP session
establishes (`connected: true`), fblock 0 goes `in_progress`, but
`farc_writes_total` for that storage stays `0` forever and nothing in
`farcd`'s logs looks like an error — just repeated `"N RTP packets lost"`
lines. Initially suspected RTP-loss recovery in `internal/ingest` (see
git history of this file for that line of investigation and the evidence
that it reproduces on both a real LAN camera and a clean mediamtx source).
**That suspicion was wrong** — RTP loss was a red herring that merely
correlated (any live RTSP source has some loss); see Root cause below.

## Root cause (confirmed via live instrumentation, 2026-08-17)

Reproduced deterministically against a clean e2e mediamtx source
(`e2e/docker-compose.e2e.yaml`), with temporary `log.Printf` instrumentation
added to `internal/storage/segment.go`'s `promoteLocked` and
`internal/storageengine/engine.go`'s `flushTriggerReadyLocked`/
`nextDeadlineLocked`/`Append`/`finishWriteLocked` (added, observed, then
fully reverted via `git checkout --` — not left in the tree).

The decisive log line:

```
finishWriteLocked engine=0xc0000d0360 job=0xc000025220 open=true closed=false
pos=0 dataLen=378 res={Corrupted:false FailedOffset:0}
err=ioengine: offset/length not aligned to required block size
```

Chain of causation:

1. `internal/api/storages.go`'s `POST /storages` handler never sets
   `Backend` explicitly unless the caller does (neither the web form nor
   `e2e/tests/setup.ts` do). `ioengine.Open`'s empty-string case
   (`internal/ioengine/open_linux.go:11`) maps `""` to **`OpenDirect`**
   (O_DIRECT), not `OpenStandard` — i.e. **O_DIRECT is the real default**,
   not an opt-in.
2. `internal/ioengine/direct_linux.go:55`: for a regular file (not a block
   device), `probeAlignment` hardcodes the required alignment to **4096**
   bytes. `DirectBackend.WriteAt` (line 82-87) rejects any offset/length
   that isn't a multiple of that.
3. `internal/storage/segment.go`'s `promoteLocked` calls
   `u.engine.EnqueueOpenWrite(offset, headerAndMagic, timeout)` with the
   **raw, unpadded** `headerAndMagic` bytes (378 bytes in this repro) as
   the job's very first `data`.
4. `internal/storageengine/engine.go`'s `stepWriteLocked` writes
   `job.data[job.pos:job.pos+chunkLen]` directly via `backend.WriteAt` —
   for this first chunk, `chunkLen = len(headerAndMagic) = 378`, not
   4096-aligned. **Every single continuous/open segment write fails on
   its very first attempt**, unconditionally, on the default backend —
   this has nothing to do with bitrate, packet loss, or camera quality.
5. The failure hits `stepWriteLocked`'s `WriteAt` error branch (line
   ~415-419), which calls `finishWriteLocked(job, WriteResult{}, err)`.
   `finishWriteLocked` (line 444-449) unconditionally does
   `close(job.done); e.writeQueue = e.writeQueue[1:]` — **it does not
   check `job.open`** before dequeuing. An ordinary (`EnqueueWrite`) job
   failing this way is correctly abandoned (its one caller does
   `ticket.Wait()` and reacts). But `segment.go`'s open/continuous path
   never waits on the ticket at all — the `*WriteHandle` returned by
   `EnqueueOpenWrite` is only ever used for further `Append`/`Written()`
   calls, with no code path that ever notices `job.err` or the job being
   dequeued.
6. Every subsequent `HandleFrame` → `AddFrames` → `WriteHandle.Append`
   call keeps mutating `job.pendingAppend` on the now-orphaned `job`
   object — accumulating in memory forever (confirmed growing past 300KB
   in a ~15s repro window) — but since `job` is no longer in
   `e.writeQueue`, `Step()`/`Run()` never looks at it again. No write, no
   error surfaced, no retry, no fblock ever marked `Bad`. The fblock
   stays `in_progress` indefinitely and `farc_writes_total` never moves,
   exactly matching every symptom originally attributed to RTP loss.

**This means**: continuous recording (`internal/ingest`'s `PolicyContinuous`,
via the shared-segment/`ADR-017` periodic-flush path) essentially never
persists a single byte on any real deployment using the default backend —
this is not a rare edge case, it is the first-write path, unconditionally.
The only reason `two-channel-playback.spec.ts`/`e2e/tests/setup.ts` was
never caught failing this way in CI (if it ever ran) is a separate,
unverified question — worth checking once a fix lands.

## Two independent defects, both need fixing

1. **The actual misalignment**: `promoteLocked`/`EnqueueOpenWrite`'s first
   chunk (`headerAndMagic`) isn't padded to the backend's alignment
   requirement before being handed to the write-verify loop, unlike
   `Init`'s one-shot full-fblock write (`internal/storage/init.go`, which
   happens to always be alignment-sized because it writes an entire
   `FblockSize`-sized buffer).
2. **The silent failure mode**: `finishWriteLocked`'s error branches in
   `stepWriteLocked` (WriteAt error, ReadAt error, corruption mismatch)
   don't distinguish an open/continuous job from an ordinary one before
   dequeuing — an open job's failure is currently unobservable by any
   caller. Even after (1) is fixed, some other future error (real disk
   failure, corruption) would still vanish silently for the continuous
   write path. This needs its own error-surfacing mechanism (mark the
   fblock `Bad` and notify, same as `WriteFcontainer`'s existing
   corruption-retry path does for the one-shot case) — `segment.go`
   currently has no code path watching for this at all.

## Not yet decided (needs its own grilling round before implementation)

- Exact fix for (1): pad `headerAndMagic` itself before
  `EnqueueOpenWrite`? Have `stepWriteLocked` pad the first chunk
  specially? Change the alignment/chunking logic to always write
  alignment-sized pieces regardless of job type?
- Exact fix for (2): what should happen when an open/continuous job's
  write fails — mark `Bad` and let the *next* frame's write trigger a
  fresh `BeginSegment` on a new index (mirroring `WriteFcontainer`'s
  retry), or something else? Who's responsible for noticing (segment.go
  needs some async completion-watching mechanism it doesn't have today)?
- Whether this also affects the one-shot `WriteFcontainer` path at all
  (it always writes a full aligned `FblockSize` buffer, so likely not,
  but worth confirming explicitly rather than assuming).
- Whether `StandardBackend` (`Alignment() == 1`) masks this bug entirely
  in any test that explicitly passes `Backend: "standard"` — if so, that
  explains why no existing unit/integration test caught this (worth
  checking `internal/storage`'s test helpers for which backend they use).
  **Confirmed**: every existing `internal/storage` test uses
  `ioengine.OpenStandard` — none exercised O_DIRECT/alignment at all.

## Fix (2026-08-17)

Both defects fixed together, per the follow-up grilling session:

1. **Alignment** (`internal/storageengine/engine.go`): `EnqueueOpenWrite`
   now pads `headerAndMagic` with zero scratch bytes up to
   `backend.Alignment()`, tracked in a new `writeJob.headerPadLen` field.
   Simpler than the originally-discussed "trim and overwrite like the
   trailer" plan: rewinding into the padding to reclaim it would leave the
   *next* write starting at an unaligned position (the trailer's own
   rewind point is always alignment-sized by construction; the header
   generally isn't). Instead the padding is permanent — at most one
   `Alignment()`-1 bytes of one-time waste per fblock, negligible against
   real fblock sizes — and `WriteHandle.Written()` now excludes both
   `trailerLen` and `headerPadLen` unconditionally, same treatment as the
   trailer gets. `internal/storage/segment.go`'s `contentBytes()` (the
   pool-status-list bar's live estimate) inherits this for free.

2. **Silent failure** (`internal/storageengine/engine.go` +
   `internal/storage/segment.go`): `WriteHandle.Append` now returns
   `error` (new `ErrWriteJobFailed` sentinel, or the job's real I/O error)
   when the job already finished on its own. `segmentImpl.pushReadyLocked`
   (called from both `AddStreamParams`/`AddFrames`, whose public
   `storage.Segment` signatures already returned errors — no interface
   change needed) propagates this into a new `failLocked`: marks the
   fblock `Bad`, closes the segment, releases its pool slot, returns
   `ErrSegmentClosed`. `internal/ingest/storagesegment.go`'s
   `StorageSegment.call` already retries on exactly that error (previously
   only for pool-rotation-by-fullness) and transparently opens a fresh
   segment via `BeginSegment` on the next call — no new retry plumbing.
   The one call site *inside* `promoteLocked` itself (flushing `s.backlog`
   right after promotion) can't use `failLocked` — it runs under `Pool`'s
   own lock, and `failLocked` calls back into `pool.release`, which would
   deadlock — so it just propagates the `Append` error directly, same as
   every other error in `promoteLocked` already does.

Existing tests fixed along the way (encoded the old, buggy behavior):
`TestEnqueueOpenWrite_SkipsWriteWhenBelowAlignment`'s `FchunkSize: 2` was
smaller than its own (now correctly padded) header, producing sub-
alignment chunk writes no real config could ever have (real `FchunkSize`
is always 4-16 MiB, header always a few hundred bytes) — bumped to 8 to
match alignment, preserving the original test's intent.

New tests: `internal/storageengine`'s `fakeBackend.WriteAt` now enforces
alignment like a real `direct` backend (previously silently accepted
anything, which is exactly how this bug went uncaught);
`TestWriteHandle_AppendReturnsErrorAfterJobFailed` (storageengine);
`TestSegment_WriteFailureMarksFblockBadAndOpensFreshSegment`
(`internal/storage`, corruption-injection style matching the existing
`corruptingBackend` pattern).

Verified live end-to-end against the real e2e stack (mediamtx +
`e2e/media/sample.mp4`, real O_DIRECT backend, not just unit tests): raw
disk bytes past the `FARCCONT` magic went from permanently all-zero (the
bug) to real encoded frame content within one flush-timeout window after
the fix. `farc_writes_total` legitimately stays 0 during this — confirmed
via `internal/storage/health.go`'s `RecordWrite` call sites that it only
counts *completed* (`Close()`'d) fblocks, not periodic mid-stream flushes;
not a sign of a remaining bug.

Full `go build`/`go vet`/`go test -race ./...` green.
