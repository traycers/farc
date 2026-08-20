# 03 — Fblock completion-rate metric + panel

Status: open

## Task

No fblock-rotation-rate metric exists. Add a counter incremented at the
exact point a fblock's state transitions to `Ready`, and a per-storage-only
panel — no total line, deliberately (spec decision 6).

1. `internal/index/manager.go:143` (`Manager.CompleteWrite`, at
   `m.catalog.SetState(idx, fblock.Ready)`) is the transition point,
   reached via `internal/storage/writetxn.go:76`'s `completeFblockWrite`
   from `internal/storage/segment.go:531` (`closeLocked`) and `:577`
   (`writeTailLocked`). Add the increment right after `CompleteWrite`
   returns successfully, via a new `HealthMonitor` method (e.g.
   `RecordFblockCompleted()`) called from `completeFblockWrite` — keep
   `internal/index` itself free of any metrics dependency, matching its
   current zero metrics-awareness.
2. `internal/api/metrics.go`: new `prometheus.NewDesc` var (label
   `[]string{"storage"}`), registered in `Describe` (lines 44-53), emitted
   as a `counter(...)` in `collectUnitMetrics` (lines 65-142) sourced from
   the new `HealthMonitor` accessor — same pattern as `farc_writes_total`
   (`metrics.go:130`).

### Dashboard

3. New `"timeseries"` panel: single target
   `rate(farc_fblocks_completed_total[5m])`, legend `{{storage}}` — no
   `sum()` total series.

## Tests

`internal/storage/health_test.go`: the new counter increments correctly
and round-trips through its new accessor. `internal/index`: confirm
`CompleteWrite`'s existing state-transition behavior is unchanged by
whatever plumbing step 1 adds around its call sites (no behavior change
expected inside `internal/index` itself).

## Verify

`go test ./internal/index/... ./internal/storage/... ./internal/api/...`;
manually: use a small `MaxChannels`/`FblockSize` test storage so fblocks
rotate quickly, record continuously, and watch the panel move.
