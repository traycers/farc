# 02 — RTSP-in vs storage-write byte volume (metrics + panel)

Status: open

## Task

No byte-volume metric exists anywhere in this repo today — `internal/ingest`
has no byte/frame counters at all, and `HealthMonitor` only counts
write/read pass-fail, never bytes (spec decisions 3-4). Add two new
counters and one combined panel comparing them.

### Backend — RTSP-in counter (`internal/ingest`)

1. `ChannelIngest` (`internal/ingest/channelingest.go:20`) has no
   `StorageID` field. Add one (e.g. `storageID string`) and set it in
   `NewChannelIngest` (currently `NewChannelIngest(channel uint16, policy
   *CapturePolicy) *ChannelIngest`, `channelingest.go:91`) by adding a new
   parameter, threaded from its only call site,
   `internal/ingest/ingestmanager.go:196`
   (`ci := NewChannelIngest(cfg.Channel, policy)` — `cfg.StorageID` is
   already in scope there).
2. Add a byte counter to `ChannelIngest` (e.g. `rtspBytesReceived
   atomic.Int64`) incremented with `len(pkt.Payload)` inside all three
   `OnPacketRTP` closures — `internal/ingest/rtsp.go:225` (video, before
   `strategy.decode(pkt)`), `:431` (G711), `:459` (AAC). This is raw RTP
   payload bytes, pre-depacketize/decode (spec decision 3) — do not move
   the counter after `decode(pkt)`.

### Backend — storage-write counter (`internal/storage`)

3. Add `RecordBytesWritten(n int)` to `HealthMonitor`
   (`internal/storage/health.go:31`, alongside the existing `RecordWrite`),
   and call it at the two success call sites where content bytes are
   already known locally: `internal/storage/segment.go:531` (`closeLocked`,
   `len(contentBuf)`) and `:577` (`writeTailLocked`, `contentBuf` param).
   Content bytes only — not catalog/TOC/prolog/epilog (spec decision 4).
   `HealthMonitor.Stats()` (`health.go:44`) needs a new return value
   alongside its existing `(writes, writeFailures, reads)`.

### Backend — Prometheus wiring (`internal/api/metrics.go`)

4. Add two new `prometheus.NewDesc` vars (label `[]string{"storage"}`,
   following `writesTotalDesc`/`writeVerifyFailuresDesc` at lines 28-29
   exactly), register both in `Describe` (lines 44-53), emit both in
   `collectUnitMetrics` (lines 65-142) via the existing `counter(...)`
   helper (lines 113-118).
   - Storage-write bytes: source from the new `HealthMonitor` return value
     added in step 3, same call site as `unit.Health().Stats()`
     (`metrics.go:111`).
   - RTSP-in bytes: `storageCollector` currently has no path from a storage
     to its channels' `ChannelIngest` state — this needs new plumbing, e.g.
     a new `IngestManager` method summing `rtspBytesReceived` across every
     channel whose `StorageID` matches, mirroring how
     `internal/api/channels.go`'s `channelCountForStorage` (added earlier
     this session) aggregates `s.ing.List()` filtered by `StorageID`.

### Dashboard (`deploy/observability/grafana/dashboards/storage-fblocks.json`)

5. One new `"timeseries"` panel, `"datasource": {"type": "prometheus",
   "uid": "prometheus"}` (matching every existing panel), 4 targets:
   `rate(farc_rtsp_bytes_received_total[5m])` (legend `rtsp in {{storage}}`),
   `sum(rate(farc_rtsp_bytes_received_total[5m]))` (legend `rtsp in total`),
   `rate(farc_storage_bytes_written_total[5m])` (legend `written
   {{storage}}`), `sum(rate(farc_storage_bytes_written_total[5m]))` (legend
   `written total`). Set `fieldConfig.defaults.unit: "Bps"` so the axis
   auto-scales KiB/s–MiB/s.

## Tests

Backend: a focused test on `ChannelIngest`'s new byte counter incrementing
per received packet; extend `internal/storage/health_test.go` to cover
`RecordBytesWritten`/the new `Stats()` field round-tripping; a
`storageCollector`/`collectUnitMetrics` test (extend whatever currently
covers `internal/api/metrics.go`, or add one) asserting both new series
appear with the `storage` label populated correctly.

## Verify

`go test ./internal/ingest/... ./internal/storage/... ./internal/api/...`;
manually: run the local dev stack (`docker compose up`), use the channel
form's "Generate" button (this session's `mediamtx`/`ffmpeg-test` addition)
to point a channel at the local RTSP test source, confirm both new series
move on the dashboard panel.
