# Backpressure frame-shedding isn't GOP-aware yet, despite being documented as such

Status: fixed (2026-08-19, via `/mattpocock-skills:tdd`)

Split out from
`.scratch/capture-keyframe-start/issues/01-first-frame-not-guaranteed-keyframe.md`'s
grilling session: explicitly out of scope there, since it's a different
concern (frame-shedding behavior *during* an already-open recording under
load) from 01's fix (guaranteeing a *new* recording starts on a keyframe).

## Problem

`docs/docs/archive/10-capture-policy.md` §4 documents that under
backpressure, the system "снижает GOP или дожидается ключевого кадра"
("reduces the GOP rate or waits for a keyframe") when shedding frames —
implying GOP-aware shedding (drop whole GOPs, resume cleanly at the next
keyframe) rather than dropping frames indiscriminately mid-GOP.

The actual implementation doesn't do this: `internal/ingest/channelingest.go`'s
`skipFrames` is a plain boolean toggle, driven by a backpressure signal
from `internal/farcd`, with no GOP/keyframe awareness in `ChannelIngest`
itself. Frames are simply skipped or not skipped per-call, which can (as
far as investigated so far) strand P-frames mid-GOP without their
reference I-frame when shedding starts or stops at an arbitrary point.

## Facts gathered during grilling (2026-08-19)

- `skipFrames()` is checked once per decoded access unit/packet, at four
  call sites in `internal/ingest/rtsp.go` (`setupH264`, `setupH265`,
  `setupG711`, `setupAAC`), with no relation to frame kind (I/P) or GOP
  position — it can in principle flip value between two consecutive
  frames of the same GOP.
- The backpressure signal itself is `internal/farcd/farcd.go`:
  `func() bool { return unit.PoolStatus() == storage.PoolBackpressure }`
  — a live, mutex-guarded, undebounced read of the storage buffer pool's
  occupancy (`internal/storage/pool.go`'s `Status()`/`statusLocked()`).
  `channelingest.go`'s doc comment describing this as
  `StorageEngine.Level()`-driven is **stale**: that signal
  (`EngineLevel()`) is metrics-only today, superseded by `PoolStatus()`.
  Default tuning (`storage/tuning.go`): `PoolBackpressure` only fires when
  all 4 buffer-pool slots are simultaneously occupied — a last-resort
  condition, not routine.
- When `skipFrames()` flips true mid-GOP today: frames already written
  before the flip are permanent (writes are synchronous, nothing rolls
  back); dropped frames never even reach `CapturePolicy`'s `FrameQueue`
  (the check happens in `rtsp.go`, before `HandleFrame` is called at all)
  — so prerecord/retention is equally blind to a shed window, not just
  the live recording. Resume takes whatever frame arrives next (I or P)
  with no keyframe check.
- `docs/docs/archive/10-capture-policy.md` §4's "снижает GOP или
  дожидается ключевого кадра" has no further elaboration anywhere in the
  docs, and §8 (Открытые вопросы) explicitly lists "which component
  decides this" as unresolved — this ticket's grilling session is what
  actually settles it.
- Existing test coverage (`channelingest_test.go`'s
  `TestChannelIngest_SkipFramesWhileBackpressureSignalTrue`) only
  exercises the audio (G711) path, which has no GOP concept — no test
  exercises `skipFrames` against the video path or a mid-GOP flip.

## Design decisions (grilling, 2026-08-19)

- **GOP-atomic shedding, video only**: `skipFrames()` is polled only when
  a decoded access unit is a keyframe (I-frame); that single read decides
  whether the *entire* upcoming GOP is recorded or dropped, until the next
  I-frame re-evaluates. A GOP is never split between "recorded" and
  "dropped" halves. Audio (no GOP concept) keeps today's per-frame
  behavior unchanged.
- **Reaction latency to a GOP is acceptable**: backpressure is a
  last-resort condition (all 4 pool slots full by default tuning) and the
  buffer pool itself already provides slack — a delay of up to one GOP's
  duration before video shedding fully engages/disengages is an
  acceptable tradeoff for never corrupting a GOP.
- **Shed frames still don't reach `FrameQueue`** — current behavior kept
  as-is. Pushing more data into memory during backpressure would work
  against the point of shedding; the resulting gap is the same class of
  already-tolerated discontinuity as a reconnect gap (issue 01).
- **No extra resume logic needed**: since a shed/recorded decision is only
  ever made at an I-frame, the first recorded frame after a shed period is
  always that next GOP's own I-frame, by construction — `keyframeGate`
  (issue 01) is not reused or extended; this is a structurally separate
  gate (a shed decision can flip arbitrarily many times within one
  already-flowing session, long past `keyframeGate`'s one-shot
  session-start check).
- **Doc cleanup**: fix `channelingest.go`'s stale `skipFrames` doc comment
  (still describes the old `StorageEngine.Level()` signal) while touching
  this file anyway.
- **Update `docs/docs/archive/10-capture-policy.md`** §4/§8 to record this
  decision, resolving the doc's own open question.
- No Plan Mode — straight to `/mattpocock-skills:tdd`.
- **Testing**: unit tests only, in `internal/ingest`, synthetic GOP
  sequences (I,P,P,I,P,...) with the backpressure signal toggled at
  various points — asserting a GOP is never split, and the first recorded
  frame after a shed period is always an I-frame. No new e2e scenario
  (same reasoning as issue 01: unreliable to reproduce a precisely-timed
  mid-GOP signal flip against a real camera).

## Fix

- `internal/ingest/rtsp.go`: new `gopShedGate` type — `allow(kind,
  skipFrames)` calls `skipFrames()` only when `kind ==
  mediatree.FrameKindI`, latching the result into `shedding`; every other
  frame of that GOP reuses the same answer regardless of what `skipFrames`
  would return by then. `setupH264`/`setupH265` each construct their own
  `shed := &gopShedGate{}` alongside their existing `keyframeGate`, and
  replaced their direct `if ci.skipFrames() { return }` check with `if
  !shed.allow(kind, ci.skipFrames) { return }`. `setupG711`/`setupAAC`
  (audio, no GOP concept) are untouched — still check `ci.skipFrames()`
  directly, per frame.
- `internal/ingest/channelingest.go`: updated the `skipFrames` field's and
  `SetBackpressureSignal`'s doc comments — they described the old,
  superseded `StorageEngine.Level()` signal; now describe the real
  `storage.Pool.Status() == storage.PoolBackpressure` wiring
  (`internal/farcd`) and `gopShedGate`'s GOP-boundary-only polling for
  video vs. audio's unchanged per-frame check.
- `docs/docs/archive/10-capture-policy.md` §4: added a paragraph recording
  the concrete meaning of "снижает GOP или дожидается ключевого кадра"
  now that it's implemented. §8: resolved the "channel connection between
  `StorageUnit` and `CapturePolicy`" open question (struck through,
  replaced with what `internal/farcd` actually wires today).

## Tests

TDD red→green:
- `internal/ingest/rtsp_gopshedgate_test.go` (new):
  `TestChannelIngest_H264_BackpressureShedsWholeGOPsNotPartial` and its
  H265 mirror — three GOPs (2 frames each); the backpressure signal flips
  true right when GOP2's keyframe arrives, then flips back to false before
  GOP2's P-frame arrives. Asserts exactly GOP1 and GOP3's frames (4 total)
  land in the fcontainer, in the exact I,P,I,P order — GOP2 is entirely
  absent (not partially recorded), proving both the "never split" and
  "resume starts exactly on the next GOP's own keyframe" properties in one
  test.
- Existing `TestChannelIngest_SkipFramesWhileBackpressureSignalTrue`
  (audio/G711, per-frame shedding) re-verified unchanged/passing —
  confirms the audio path's scoping decision wasn't disturbed.

`go build ./...`, `go test ./...` (full repo, 32 packages green),
`go test ./internal/ingest/... -race` (clean), `gofmt -l` (clean), and
`golangci-lint run ./internal/ingest/...` (clean).
