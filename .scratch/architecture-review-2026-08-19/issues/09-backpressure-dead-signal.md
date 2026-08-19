# Two backpressure signals exist; one looks structurally unreachable

Status: verified, minimal fix applied (2026-08-19) — traced every EnqueueWrite/EnqueueOpenWrite call site; chose (a), rejected (b)

## Problem

`internal/storageengine/engine.go:410-420`'s quota-withdrawal branch
(triggered at `LevelBackpressure`, ADR-011's write-priority mechanism) may
be unreachable under the current `Pool` design: `Engine`'s own comment
(`engine.go:518`) states only one open job ever exists per `Engine`, and
every write call site in `internal/storage/segment.go` waits on its
ticket (`ticket.Wait()`) before enqueueing the next — so `writeQueue`
never approaches `internal/storage/tuning.go`'s `BackpressureAt: 16`.
`CONTEXT.md`'s "Уровни Backpressure" entry already documents Pool
occupancy as the signal that superseded engine-queue depth for this
purpose, but `Unit.EngineLevel()` (`internal/storage/unit.go:95`) is still
exposed as a metric a caller could mistake for the live signal.

**Not a violation of ADR-011** — just possibly dead code given how `Unit`
drives the engine today. The exploring agent flagged this as verified only
at the level of "every write call site waits on its ticket before
enqueueing the next"; it did not trace every `EnqueueWrite` call site
(e.g. hypothetical concurrent multi-segment futures) that could someday
make the branch reachable. Confirm that before removing anything.

## Verification (2026-08-19)

Traced every `EnqueueWrite`/`EnqueueOpenWrite` call site in the codebase (4
total): `internal/storage/init.go`'s one-time, single-threaded init write,
and `internal/storage/segment.go`'s three (`promoteLocked`'s
`EnqueueOpenWrite`, `closeLocked`'s retry-loop `EnqueueWrite`,
`writeTailLocked`'s `EnqueueWrite`) — every one synchronously awaits its
own ticket before the next is ever enqueued. `WriteHandle.Append` (the
periodic-flush path, ADR-017) doesn't enqueue a second job either — it
appends to the *same* already-queued job's `pendingAppend` buffer, which
the engine's own background worker drains. So `writeQueue` depth is
provably ≤1 given every current caller, confirming the original finding:
`BackpressureAt: 16` is unreachable today.

**Chose (a), rejected (b).** Read `Engine.Step()`'s actual quota-withdrawal
code (`engine.go:409-420`): it's correctly-written, ADR-011-compliant
defensive logic (`quotaRemaining = 0; chunksWritten = 0` when
`level == LevelBackpressure`) for a scenario current usage just never
triggers — not broken, not misleading in its own file, and would become
load-bearing again if a future change ever pipelines more than one write
job per `Storage` (e.g. multi-segment concurrent flushing). Removing it
would trade a real (if currently dormant) safety net for a few lines of
locality, with no live bug it's causing today. `Unit.EngineLevel()`'s own
doc comment already scoped its one consumer to `MetricsEndpoint`, and
`CONTEXT.md`'s "Pool" glossary entry already states Pool occupancy
superseded engine-queue depth for behavioral gating — the ambiguity this
ticket worried about was already resolved at the domain-glossary level,
just not cross-referenced from the code itself.

No ADR addendum needed — ADR-011's decision was never violated, only its
trigger condition never reached under current write patterns, which isn't
a decision the ADR itself needs to record.

## Fix (2026-08-19)

`internal/storage/unit.go`: `EngineLevel()`'s doc comment extended with an
explicit "metrics only, not a live behavioral signal — Pool occupancy is"
cross-reference to `CONTEXT.md`'s "Pool" entry, plus the one-sentence
reachability fact this investigation confirmed. No other code changed —
`Engine.Step()`'s quota-withdrawal logic is left exactly as it was.
`go build ./...`, `go test ./internal/storage/...`, `gofmt -l` all clean.

## Comments
