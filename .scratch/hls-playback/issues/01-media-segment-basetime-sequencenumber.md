# CMAF media segments never set tfdt baseMediaDecodeTime or a real mfhd sequence_number

Status: fixed (2026-08-18)

## Symptom

User reported (real production stack, not e2e): playing an archived
recording in the web player failed partway through with

```
Playback failed (mediaError/bufferAppendError)
```

(hls.js's `ErrorTypes.MEDIA_ERROR`/`ErrorDetails.BUFFER_APPEND_ERROR`,
surfaced via `PlayerPage.tsx`'s `hls.on(Hls.Events.ERROR, ...)` handler.)

## Investigation

`docker logs farc-hls_server-1` showed every segment/init/playlist request
returning `200` — no server-side error, so the bug had to be in the
*content* of an otherwise successfully-served segment, not a request
failure. Confirmed farcd (`farc-farc-1`) was already running that day's
latest commit (image built within a minute of the last commit), while
hls_server's image was a day older — but `internal/segment`/`internal/
hlsapi` (the only packages relevant to this bug) hadn't been touched that
day, so this wasn't a stale-build issue either.

Fetched the actual served `init.mp4`/`seg.m4s` bytes directly (via a
`curlimages/curl` container on the compose network, bypassing this
sandbox's own Squid-proxy interception of host↔container HTTP) and parsed
the raw ISOBMFF boxes by hand (no ffprobe/mp4box needed — `moof`/`mfhd`/
`traf`/`tfhd`/`tfdt` are simple enough to walk directly). Found:

- Every segment's `mfhd.sequence_number` was **1** — including the
  *second* segment of the same recording.
- Every segment's `tfdt.baseMediaDecodeTime` was **0** — for every track,
  every segment, regardless of position in the timeline.

## Root cause

`internal/segment/media.go`'s `BuildMedia` constructed
`&fmp4.PartTrack{ID: videoTrackID, Samples: samples}` (and the audio
equivalent) without ever setting `PartTrack.BaseTime` — a real field the
`mediacommon/v2/pkg/formats/fmp4` library exposes specifically to encode
`tfdt.baseMediaDecodeTime`, just never read here. Likewise `fmp4.Part{
SequenceNumber: 1, ...}` was a **hardcoded literal**, never parameterized
by which segment index was actually being built.

hls.js's playlist/CMAF handling for an already-fragmented source (this
one, via `#EXT-X-MAP`) is a byte passthrough — it forwards each fetched
segment straight to the browser's own native MSE fMP4 demuxer, which is
what actually parses `moof`/`tfdt`/`mfhd` and positions each fragment's
samples on the `SourceBuffer`'s timeline. With every fragment claiming
`baseMediaDecodeTime=0`, every fragment's samples decode-position at time
zero and directly overlap every other fragment's; with `mfhd.sequence_
number` frozen at 1, the browser's native demuxer sees the same fragment
sequence number repeated forever. Either is enough on its own to break
`SourceBuffer.appendBuffer()` in a real browser — exactly `mediaError/
bufferAppendError` — despite every segment individually being
well-formed, self-consistent ISOBMFF (which is why the earlier isolated
`ffprobe`/box-walk of a single segment looked fine; the bug only shows up
across *multiple* fragments appended to the same `SourceBuffer`, i.e.
real sequential playback).

Not related to any of this session's other fixes (issue 07's stream
merging, msm audio vaa-blocks, Width/Height) — `internal/segment`/
`internal/hlsapi` weren't touched by any of that work, and this bug's
mechanism (fMP4 fragment header fields) is entirely orthogonal to the
fcontainer tree shape.

No existing test caught this: `internal/segment/segment_test.go`'s
`TestBuildMedia_PartitionsAcrossTwoSegmentsWithNoOverlap` already builds
exactly two consecutive segments and round-trips them through `fmp4.Parts.
Unmarshal`, but never asserted on `BaseTime`/`SequenceNumber` — only on
sample durations/payloads.

## Fix

`internal/segment/media.go`:
- `BuildMedia` gained a `segIndex int` parameter (already available at its
  one call site, `internal/hlsapi/handlers.go`), used as `SequenceNumber:
  uint32(segIndex + 1)` (1-based, matching the previous hardcoded starting
  value for segment 0).
- Each `PartTrack` now sets `BaseTime: videoFrames[0].Time` /
  `audioFrames[0].Time` — the segment's own first included frame's real
  absolute ns timestamp for that track, consistent with how sample
  durations are already computed from absolute frame times elsewhere in
  this file.

`internal/hlsapi/handlers.go`: passes the already-in-scope `segIndex`
through to `BuildMedia`.

**Test**: extended `TestBuildMedia_PartitionsAcrossTwoSegmentsWithNoOverlap`
to assert both segments' `BaseTime` (0 and 1_000_000, matching the
fixture's frame times) and that their `SequenceNumber`s differ. Red before
the fix (`BaseTime` came back 0 for the second segment), green after.

**Live verification**: rebuilt and redeployed the real `hls_server`
container, cleared its (purely regenerable) disk segment cache (the
`hls_cache` named volume — otherwise it would keep serving the
already-cached pre-fix bytes for this exact recording), re-fetched
segments and confirmed `mfhd.sequence_number` now increments (1, 2, 3...)
and `tfdt.baseMediaDecodeTime` now carries real, monotonically increasing
absolute-ns values per track/segment. Then drove the actual `/player`
page end-to-end via a real headless Chromium (Playwright): searched,
played the same recording, called `video.play()`, and confirmed
`currentTime` advanced continuously (0 → 14.9s over 15s of wall time,
buffered range growing from ~31s to ~45s) with zero console errors and no
`bufferAppendError` -- across multiple segment boundaries, which is
exactly the condition the bug required to reproduce.
