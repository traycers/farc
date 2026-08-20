# 04 — Per-fblock catalog/TOC/content size metrics + XY Chart panel

Status: resolved

## Task

No catalog/TOC/content per-fblock size data is exposed anywhere outside a
momentary local variable during the write path — see spec decisions 7-9
for why this must be emitted going-forward at fblock-completion time
rather than read from disk on demand (storages run up to "several million"
fblocks per `docs/docs/archive/00-requirements.md`; ADR-007 exists
specifically to avoid a full header scan, which reading every fblock's
prolog+epilog on every Prometheus scrape or REST call would reintroduce).

### Backend — plumb the sizes through (`internal/storage`, `internal/index`)

1. At the same two call sites issue 03 instruments
   (`internal/storage/segment.go:531` `closeLocked`, `:577`
   `writeTailLocked`), the three sizes are already local: `len(contentBuf)`,
   `len(tocBuf)`, and the fblock's catalog size (`h.Prolog.CatalogSize`
   from `beginFblockWrite`'s header at `closeLocked`'s call site around
   line 507; `s.catalogSize`, a struct field set at `segment.go:210`, at
   `writeTailLocked`'s). Extend `completeFblockWrite`
   (`internal/storage/writetxn.go:74`) and `Manager.CompleteWrite`
   (`internal/index/manager.go:137`) to accept `catalogSize, tocSize,
   contentSize uint32` alongside their existing `idx, uuid, begin, end`
   params, threaded through from both call sites.
2. Record the three sizes via a new `HealthMonitor` method (e.g.
   `RecordFblockSizes(idx uint32, catalogSize, tocSize, contentSize
   uint32)`), called right after `CompleteWrite` succeeds — same call
   sites as issue 03's `RecordFblockCompleted()`; land both issues'
   `HealthMonitor` calls together if implementing back-to-back, since
   they share the exact same call sites.

### Backend — Prometheus wiring (`internal/api/metrics.go`)

3. Three new `prometheus.NewDesc` vars, labels `[]string{"storage",
   "fblock"}` — the first two-label metrics in this file (every existing
   `farc_*` desc is `storage`-only). `collectUnitMetrics` (lines 65-142)
   needs a structurally new code path here: instead of one `gauge(...)`
   call per storage (the existing pattern for every other metric in this
   file), emit one `gauge(...)` call *per fblock* the `HealthMonitor`
   currently holds sizes for (only fblocks completed since this feature
   shipped — no backfill, per spec decision 7). Size the new
   `HealthMonitor` accessor accordingly (e.g. return a slice/map of
   `{index, catalogSize, tocSize, contentSize}` rather than a single
   value).

### Dashboard (`deploy/observability/grafana/dashboards/storage-fblocks.json`)

4. New `"xychart"` panel (Grafana-core panel type since 9.x — confirmed
   nothing in `docker-compose.yaml`'s `grafana` service installs plugins
   today, so no compose change needed). Three targets, each `topk(50,
   farc_fblock_<catalog|toc|content>_size_bytes{storage=~"$storage"})`,
   combined via a `"transformations": [{"id": "joinByField", "options":
   {"byField": "fblock", "mode": "outer"}}]` block (no existing dashboard
   JSON in this repo uses `transformations` — author this from scratch
   against Grafana 11's schema). Render as 3 line series (not stacked
   bars, per spec decision 9) — X = `fblock`, Y = bytes
   (`fieldConfig.defaults.unit: "bytes"`).

## Tests

`internal/storage`: new sizes are recorded and retrievable correctly for
both the `closeLocked` and `writeTailLocked` completion paths.
`internal/index`: extend existing `internal/index/manager_test.go`
`CompleteWrite` calls with the new params, confirm state-transition
behavior is otherwise unchanged. `internal/api`: `collectUnitMetrics`'s new
per-fblock emission produces exactly one series per completed fblock for a
test with a known number of completions.

## Verify

`go test ./internal/index/... ./internal/storage/... ./internal/api/...`;
manually: run the local dev stack, let a channel complete a handful of
fblocks on a small `FblockSize`/`N` test storage, confirm the XY Chart
plots a flat catalog line and varying toc/content lines for those
fblocks.

## Comments

2026-08-20: Implemented. Added `FblockSizeRecord`/`fblockSizes []FblockSizeRecord`
(mutex-guarded) + `RecordFblockSizes`/`FblockSizes()` to `HealthMonitor`
(`internal/storage/health.go`, `health_test.go`); extended
`completeFblockWrite`'s signature with `catalogSize, tocSize uint32` (landed
alongside issue 02's `contentSize`), threaded from both `segment.go` call
sites (`h.Prolog.CatalogSize`/`len(tocBuf)` at `closeLocked`,
`s.catalogSize`/`len(tocBuf)` at `writeTailLocked`). Added the first
two-label (`storage`,`fblock`) descs to `internal/api/metrics.go` plus a
new per-fblock emission loop in `collectUnitMetrics` (one gauge triplet per
completed fblock, not per storage); test asserts exactly one series per
completion, matching label counts (`prometheus/client_golang` sorts labels
alphabetically -- `fblock` before `storage` -- which the test asserts
against). Added the `"xychart"` panel with `topk(50, ...)` targets + a
`joinByField` transform on `fblock` (authored from scratch, no automated
test per this repo's convention for dashboard JSON -- verify by manual
inspection in Grafana).

Post-implementation review caught that the original append-only
`[]FblockSizeRecord` design breaks on a cyclic storage's normal steady
state: any physical fblock index gets completed more than once over the
storage's lifetime, which would emit two Prometheus metrics with identical
`{storage,fblock}` labels in one scrape -- `promhttp.HandlerFor`'s default
`HTTPErrorOnError` turns that into a failed `/metrics` scrape for *every*
metric, not just this feature's. Fixed by making `fblockSizes` a
`map[uint32]FblockSizeRecord` (last-write-wins per index), with
`FblockSizes()` returning entries sorted by index for a stable scrape.
New tests: `TestHealthMonitor_RecordFblockSizes_ReusedIndexOverwritesNot
Appends`, `_ReturnsRecordsSortedByIndex`, and an end-to-end
`TestHandleMetrics_SurvivesFblockIndexReuse` (N=1 storage, two writes,
confirms `/metrics` still returns 200).

Tests green, full suite + `golangci-lint run` clean.
