# Fire CapturePolicy/ChannelIngest hooks after releasing the lock, not under it

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

`internal/ingest/policy.go`'s `onRecordingChange` (fired from
`openSegmentLocked`/`closeSegmentLocked` while `p.mu` is held) and
`internal/ingest/channelingest.go`'s `onConnectionChange` (fired from
`setConnected` while `connMu` is held) are plain `func(...)` values invoked
synchronously under the owning type's own lock. Nothing stops a hook from
calling back into a method that needs that same lock.

This already happened: `internal/farcd/farcd.go`'s hooks need a channel's
Storage id, but can't call `IngestManager.List()` (which calls
`policy.Policy()`, needing `p.mu`) from inside a hook without risking
deadlock — `internal/ingest/ingestmanager.go`'s `StorageOf` exists
specifically as a lock-safe alternative, guarded by its own dedicated
regression test
(`TestIngestManager_OnRecordingChange_StorageOfDoesNotDeadlock`). Every
future hook consumer has to independently know this rule; it's enforced by
convention/tribal knowledge, not by the interface.

## Design (settled via grilling, 2026-08-19)

**Ordering.** `openSegmentLocked` currently fires the hook *before* its
replay loop (still under `p.mu`). Safely releasing the lock first would
require either firing after the whole locked operation (replay included)
completes, or a lock-drop-and-reacquire mid-function. Grilling picked the
former: the hook now fires once, unconditionally (even if replay fails —
preserving the pre-existing guarantee), after `p.mu` is released — at the
cost of `farcd.go`'s WS journal event landing after replay completes
instead of before it, which is immaterial for a best-effort, no-catch-up
delivery consumer that only reports a timestamp field. `closeSegmentLocked`
has no work after the flip, so this costs nothing there.

**`StorageOf`'s fate.** Kept, since it's still a legitimately cheaper O(1)
single-channel lookup than `List()`'s O(n log n) build-and-sort — just no
longer *required* for deadlock avoidance, since hooks can now safely call
`List()` too. Its doc comment (and `farcd.go`'s call-site comments)
rewritten to drop the now-stale "required to avoid deadlock" framing.

## Fix (2026-08-19)

Implemented via TDD:

- `internal/ingest/policy.go`: `openSegmentLocked` and `closeSegmentLocked`
  no longer call `onRecordingChange` themselves (`closeSegmentLocked` also
  dropped its now-unused `now` parameter and instead returns `bool` — did
  this call site actually flip `p.recording`, so callers know whether to
  fire). `StartRecording`/`StopRecording`/`Trigger`/`Tick`/`Close` all
  restructured from `defer p.mu.Unlock()` to explicit lock/unlock: capture
  `hook, channel := p.onRecordingChange, p.channel` while still locked,
  unlock, then fire.
- `internal/ingest/channelingest.go`: `setConnected` gets the same
  treatment (capture hook+channel, unlock, fire).
- `internal/ingest/ingestmanager.go`: `StorageOf`'s doc comment rewritten;
  `internal/farcd/farcd.go`'s two hook call sites' comments updated to
  match.
- New tests (TDD, red before the fix): `TestOnRecordingChange_CanSafelyCallBackIntoPolicy`
  and `TestChannelIngest_OnConnectionChange_CanSafelyCallBackIntoConnected`
  (both: hook calls a method needing the same mutex, with a 2s timeout
  guard against a real deadlock hanging the test suite) —
  `TestOnRecordingChange_FiresEvenWhenReplayFails` (characterization test,
  already passing pre-fix, locking in the unconditional-fire guarantee
  through the refactor). `internal/ingest/ingestmanager_test.go`'s old
  `TestIngestManager_OnRecordingChange_StorageOfDoesNotDeadlock` rewritten
  to `TestIngestManager_OnRecordingChange_CanSafelyCallList` — a strictly
  stronger regression (the hook now safely drives the *full* `List()` API,
  not just `StorageOf`).
- No behavior change beyond the documented hook-timing shift: full
  `go test ./...` green, `go test -race` on `internal/ingest`/
  `internal/farcd` green, `golangci-lint run` on both shows 0 issues.
  `gofmt -l` clean.

## Comments
