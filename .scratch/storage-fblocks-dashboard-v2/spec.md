# Storage & Fblocks dashboard v2 — traffic volume, fblock fill rate, per-fblock section sizes

Status: open — grilled 2026-08-20

Redesign of the "Storage & Fblocks" Grafana dashboard
(`deploy/observability/grafana/dashboards/storage-fblocks.json`) — prune it
down to just the write-verify panel and rebuild the rest around three new
graphs. Filed as one directory because all three new graphs land on the
same dashboard file and two of them (fblock completion rate, per-fblock
section sizes) share the exact same backend instrumentation point.

## Decisions (settled during grilling)

1. Scope is `storage-fblocks.json` only. The other three provisioned
   dashboards (`services-overview.json`, `logs.json`,
   `http-performance.json`) are untouched — nothing in the request concerns
   services/logs/HTTP.
2. Of `storage-fblocks.json`'s 5 panels, remove `id: 1` ("Fblock states by
   storage"), `id: 2` ("Write queue depth"), `id: 3` ("Write queue status
   (0=normal, 1=warning, 2=backpressure)"), `id: 5` ("Channel registry
   usage"). Keep `id: 4` ("Write-verify failures / sec",
   `rate(farc_write_verify_failures_total[5m])`) exactly as-is.
3. New counter `farc_rtsp_bytes_received_total{storage}` = raw RTP payload
   bytes (`len(pkt.Payload)`), counted at `gortsplib`'s `OnPacketRTP`
   callbacks (`internal/ingest/rtsp.go:225,431,459`) — *before*
   depacketization/decode. Deliberately the wire-level number, not decoded
   frame bytes: the point of this graph is comparing "what came in" against
   decision 4's "what got written," and that comparison only means
   something if "came in" is measured at the same layer for every codec.
4. New counter `farc_storage_bytes_written_total{storage}` = bytes of
   fblock *content* actually written to disk, sourced from the same
   `contentBuf`/`tocBuf` already computed locally at
   `HealthMonitor.RecordWrite`'s two success call sites
   (`internal/storage/segment.go:531`, `:577`) — deliberately content bytes
   only, not catalog/TOC/prolog/epilog overhead, since this metric is about
   payload throughput, not fblock-format bookkeeping (that's decision 7).
5. Both counters feed **one combined panel** (not two): per-storage `rate()`
   lines for both `farc_rtsp_bytes_received_total` and
   `farc_storage_bytes_written_total`, plus a `sum()` "total" line for each
   — the request's own phrasing ("объём входящего трафика по rtsp и объём
   записываемый в хранилище... по каждому хранилищу и суммарный") describes
   one comparison, not two independent graphs.
6. "Скорость заполнения фблоков" is a *different* signal from decision
   4's byte rate — it's how fast a storage rotates through its fblocks
   (`in_progress` → `ready` transitions/sec), not bytes/sec. New counter
   `farc_fblocks_completed_total{storage}`, incremented at the exact point
   a fblock's state actually flips to `Ready`:
   `internal/index/manager.go:143` (`Manager.CompleteWrite`,
   `m.catalog.SetState(idx, fblock.Ready)`), reached via
   `internal/storage/writetxn.go:76` from
   `internal/storage/segment.go:531,577`. Its panel is per-storage only —
   **no total line** — the request explicitly said "по каждому хранилищу"
   for this one without "суммарный" (unlike decision 5's pair), and a
   summed fblock-completion rate across storages with different
   `MaxChannels`/`FblockSize` geometries wouldn't mean anything anyway.
7. Catalog/TOC/content section sizes per fblock: no runtime cache of these
   exists anywhere (`fblock.Catalog`'s in-memory SoA — the thing ADR-007's
   SSD snapshot avoids re-scanning — carries only
   state/UUID/begin/end/protected, never section sizes; every existing
   reader, e.g. `internal/storage/consistency.go`'s `verifyWriteCompletion`,
   re-reads a fblock's prolog+epilog fresh off disk on demand). With
   storages documented at up to "several million" fblocks
   (`docs/docs/archive/00-requirements.md`, ADR-007), neither a
   one-Prometheus-series-per-fblock metric scraped from a full on-demand
   disk scan, nor a REST endpoint that reads every fblock on request, is
   viable. Resolution: emit these sizes **once, going forward, at the same
   `CompleteWrite` transition as decision 6** — `contentBuf`/`tocBuf`
   lengths and the fblock's `CatalogSize` are already known local values at
   that exact call site (`segment.go:531,577`), so this needs zero disk
   reads. No backfill for fblocks that were already `Ready` before this
   metric ships — the graph only shows fblocks completed after this lands.
8. New gauges `farc_fblock_catalog_size_bytes{storage,fblock}`,
   `farc_fblock_toc_size_bytes{storage,fblock}`,
   `farc_fblock_content_size_bytes{storage,fblock}` — the first two-label
   metrics in `internal/api/metrics.go` (every existing `farc_*` metric is
   `storage`-only).
9. `catalog_size` is a pure function of a storage's fixed geometry
   (`fblock.CatalogSize(C, N)`, `fblock/catalog.go:51-54`) — it's identical
   for every fblock in a given storage, not something that varies per
   index. The panel still plots it (as a flat reference line); the useful
   signal is `toc`/`content` diverging from each other and from that
   constant.
10. Rendered as a Grafana **XY Chart** panel (`"xychart"`, core panel type
    since Grafana 9.x — nothing installs plugins in this repo's Grafana
    service today, so no compose change needed), not a stacked bar chart:
    three separate line series (catalog/toc/content) are easier to compare
    than stacked segments, whose middle/top layers sit on a shifting
    baseline and hide trend. Data: `topk(50, ...)` instant queries per
    metric, merged into one table via a `joinByField` transform on the
    `fblock` label (no existing dashboard in this repo uses a
    `transformations` block — authored from scratch).
11. Filed as **4 separate issues** in this one directory:
   - `01` — prune the 4 panels (pure dashboard JSON, no code)
   - `02` — RTSP-in / storage-write byte counters + combined panel
     (decisions 3-5)
   - `03` — fblock completion-rate counter + panel (decision 6)
   - `04` — catalog/TOC/content size gauges + XY Chart panel (decisions
     7-9)

   `03` and `04` share the exact same `CompleteWrite` instrumentation
   point but stay separate: `03` is a plain counter increment, `04` plumbs
   three real byte values through `completeFblockWrite`/`CompleteWrite`'s
   signatures plus a from-scratch Grafana transform — different size and
   risk, worth reviewing/testing independently even though both touch the
   same call sites.

## Issues

- `issues/01` — Prune Storage & Fblocks dashboard down to write-verify only
- `issues/02` — RTSP-in vs storage-write byte volume (metrics + panel)
- `issues/03` — Fblock completion-rate metric + panel
- `issues/04` — Per-fblock catalog/TOC/content size metrics + XY Chart panel
