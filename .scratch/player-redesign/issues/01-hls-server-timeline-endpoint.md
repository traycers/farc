# hls_server: per-channel video-presence timeline endpoint

Status: fixed (2026-08-18, via `/mattpocock-skills:tdd`)

See `.scratch/player-redesign/spec.md` for the full design conversation
this was split from.

## Goal

A new HTTP endpoint on `hls_server` (`internal/hlsapi`) that returns, for a
list of channels and a time range, the merged list of "this channel had
video" segments — precomputed ahead of time from TOC data hls_server
already receives over WS, not computed on the fly per request.

## Behavior

- **Input**: a list of channel IDs + `[t1, t2]` (same style as the existing
  `playlist`/`candidates` routes — unix ns).
- **Output**: per channel, an ordered list of `{begin, end}` (unix ns)
  video-presence segments overlapping `[t1, t2]`, clipped to that range.
- **Segment definition**: a run of the channel's video frames where every
  consecutive gap is `< vaablocks.GapThresholdNS` (2s). A gap `>=`
  threshold starts a new segment. A segment is never computed across an
  fblock boundary (matches `vaablocks.Compute`'s existing constraint) — two
  segments from adjacent fblocks may end up flush against each other with
  no gap; that's fine, they're still reported as two entries.
- **No audio, no color/type coding, no underlying record UUIDs** in the
  response — see spec.md's "Timeline semantics" for why each of these was
  ruled out.
- Bounded by whatever `tocindex` currently holds for that channel (the
  existing storage-catalog retention window) — no separate retention
  policy to design.

## Design decision: no dependency on `internal/vaablocks`

Grilling flagged one correction to the shape suggested above: `hls_server`
must not import `internal/vaablocks` at all, even though the package
happens not to import any msm-specific types — `vaablocks` exists purely
for the msm_server integration, and pulling it into hls_server would wire
two independently-owned domains together for a saved dozen lines. Instead,
`internal/tocindex` carries its own small, independent copy of the
gap-splitting scan, and its own copy of the 2-second threshold constant
(`gapThresholdNS`, deliberately not `vaablocks.GapThresholdNS`). This
version is actually simpler than `vaablocks.Compute`: it only needs frame
timestamps, not `vaablocks.Block`'s byte-offset/config/stream resolution
(`Offset`/`Size`/`ConfigID`/`StreamID` are msm-only concerns), so there's no
error path either.

## Fix

- `internal/tocindex/videopresence.go` (new): `type Segment struct{ Begin,
  End uint64 }` and `func VideoPresenceSegments(c *toc.Columns, channel
  uint16) []Segment` — finds the channel's node (reusing the package's own
  existing unexported `findChannelNode`), scans `RoleFrameTimeVideo` within
  its subtree, and splits runs on a `>= gapThresholdNS` gap, exactly
  mirroring `vaablocks.Compute`'s loop shape but without its byte/config
  bookkeeping.
- `internal/tocindex/index.go`: `Record` gained a `VideoSegments []Segment`
  field (computed by the caller before `Insert`, same pattern as
  `Begin`/`End`); `ChannelIndex` gained `Timeline(t1, t2 uint64) []Segment`,
  which walks `Records(t1,t2)` (already sorted, already overlap-filtered)
  and clips each record's `VideoSegments` to `[t1,t2]`. Segments from
  different records are never merged into each other — two flush segments
  from adjacent fcontainers just both appear in the output, as designed.
- `internal/tocindex/subscriber.go`: `indexContainer` now also calls
  `VideoPresenceSegments(columns, ch)` and stores the result on the
  `Record` it inserts — this runs on both the live WS-push path
  (`handleWriteCompleted`) and the full-catalog `bootstrap()` rescan, so
  history and live updates share one code path, and `handleDeleted`'s
  existing `Remove(uuid)` retracts a record's segments for free (they live
  on the `Record` itself, not a separate structure).
- `internal/hlsapi/server.go` + `handlers.go`: new route
  `GET /timeline?channels=1,2&t1=..&t2=..` → `handleTimeline`. Response:
  `[{"channel":1,"segments":[{"begin":..,"end":..}]}, ...]`. A channel not
  in `s.channels` (this hls_server isn't configured to serve it) is
  silently omitted from the response rather than 404ing the whole batch.
  `internal/hlsapi/helpers.go` gained `parseChannelList` (comma-separated
  `?channels=`) and a small `writeJSON` helper.

## Tests

All three seams from the grilling session, TDD red→green:
- `internal/tocindex/videopresence_test.go`:
  `TestVideoPresenceSegments_MergesSmallGapsSplitsLargeGaps` (a <2s gap
  merges, a >=2s gap splits, using the same `second`-scale fixture style as
  `vaablocks_test.go`) and `TestVideoPresenceSegments_NoVideoForChannel`
  (audio-only channel → `nil`).
- `internal/tocindex/index_test.go`:
  `TestChannelIndex_Timeline_ConcatenatesAndClips` (two records' segments
  concatenate in time order, clip correctly at both `t1` and `t2`, and
  `Remove` retracts a record's contribution).
- `internal/tocindex/subscriber_test.go`:
  `TestEventSubscriber_IndexesVideoPresenceSegments` — the wiring seam
  between the two: drives a real `EventSubscriber` against a real farcd
  fixture and asserts `Timeline()` reflects the real decoded TOC, not just
  that the pure function and the storage method work in isolation.
- `internal/hlsapi/timeline_test.go`:
  `TestServer_Timeline_MultiChannelBatchQuery` — real HTTP round trip
  (`httptest`) for two configured channels plus one deliberately
  unconfigured channel in the same request, asserting the JSON shape and
  that the unconfigured channel is omitted, not erroring.

`go build ./...`, `go test ./...` (full repo), and `golangci-lint run
./internal/tocindex/... ./internal/hlsapi/...` all clean.
