// Package api implements internal/api from PLAN.md's Phase 10: a minimal
// HTTP/WS/metrics surface over internal/storage and internal/ingest. Unlike
// every earlier phase, no design doc in docs/docs/archive/ specifies this
// package at all — PLAN.md's own "Minimal HTTP/WS/metrics API" section (new
// design, written during planning) is the only spec, and this package
// implements it close to literally:
//
//   - StorageRegistry (registry.go) — id -> *storage.Unit, the thing every
//     other handler resolves {id} path values against. Deliberately holds
//     only already-open Units, not lifecycle (no background Init/Open
//     queue) — POST /storages runs Initializer and Startup synchronously in
//     the request goroutine, matching the sketch's "runs Initializer
//     inline". A real multi-TB Init would block a request for a long time
//     in production, but ADR-006 (lazy initialization) already makes Init
//     itself fast (only fblock 0 is touched) — that's what makes "inline"
//     viable here at all, not a v1 shortcut.
//   - HttpApiServer (server.go) — routes registered on a stdlib
//     http.ServeMux (Go 1.22+ method+wildcard patterns), no router
//     dependency needed.
//   - EventPushServer (eventpush.go) — one WS endpoint, subscribe-on-connect
//     per the sketch, backed by NotificationBus per Storage.
//   - MetricsEndpoint (metrics.go) — hand-rolled Prometheus text exposition
//     (no client library dependency) against the exact metric names in
//     docs/docs/archive/02-storage.md §8's table.
//
// Design choices not spelled out by the sketch, decided here:
//
//   - Read endpoints (toc/content) return raw bytes (application/
//     octet-stream), not JSON — ADR-004's model is "the consumer parses the
//     TOC itself and farcd just returns bytes at given offsets"; wrapping
//     that in JSON (base64) would only add cost for the documented primary
//     path. GET .../toc re-encodes the already-validated Columns via
//     toc.Encode rather than returning Unit.ReadTOC's raw disk read
//     directly, since Reader (storage package) never keeps that raw buffer
//     around — the round trip through Encode also doubles as an extra
//     validity check for free.
//   - GET .../resolve (ADR-016 fallback) is the one exception: JSON with
//     base64 frame payloads, because its entire purpose is serving a
//     consumer that has *no* parsed TOC to interpret raw bytes against —
//     the response has to be self-describing.
//   - The WS push (EventPushServer) can only filter fblock-level events by
//     channel using the *catalog's* channel_bitmap (an approximation: a
//     bitmap bit only says "some data for this channel might be in this
//     fblock", not "exactly this frame is") — precise mid-fcontainer
//     filtering doesn't exist below TOC-scan granularity, which the push
//     path deliberately avoids (that cost belongs to GET .../resolve, an
//     explicit pull, not an unsolicited push to every subscriber).
package api
