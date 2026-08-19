# A channel's first recorded video frame isn't guaranteed to be a keyframe

Status: fixed (2026-08-18, via `/mattpocock-skills:tdd`)

## Problem

Reported 2026-08-18 via the fblock tree viewer (right after
`.scratch/fblocks-ui/issues/12-tree-codec-frame-kind-and-precise-time.md`
made `frame_kind` readable as `I`/`P` instead of a raw byte): the very
first `frame(video)` recorded for a freshly-added continuous-policy
channel showed `frame_kind = P`, not `I`. Example from the tree:

```
frames(video) (void) [-]
└── frame(video) (group) = 958 frames [-]
    └── frame(video) (void) [-]
        ├── frame_data(video) (bytes) size=18132
        ├── frame_time(video) (timestamp) = 2026-08-18T17:19:04.633042
        └── frame_kind (uint8) = P
```

Since a decoder can't produce a correct picture from a P-frame without its
reference I-frame, and this is the first frame of the whole recording (no
earlier frame exists to reference), this is a real capture-correctness gap
that the tree UI just happened to make visible for the first time — not a
display bug.

## Investigation

No gate exists anywhere in the pipeline that waits for a keyframe before
starting to record:

- `internal/ingest/rtsp.go`'s `setupH264`/`setupH265`: every decoded access
  unit defaults `kind := mediatree.FrameKindP`, flips to `FrameKindI` only
  if that specific AU happens to contain an IDU/random-access NALU, and is
  then unconditionally handed to `ci.policy.HandleFrame(...)`. This applies
  identically on **first connect and every reconnect** (`run()`, called
  from both the initial path and `runReconnecting()`) — nothing between
  `Play()` and frame delivery inspects or waits on frame kind.
- `internal/ingest/policy.go`'s `openSegmentLocked` (what starts a channel
  contributing to a — possibly brand-new — fcontainer) filters replayed
  queue frames purely by **time**, never by kind.
- `internal/ingest/queue.go`'s GOP-atomic eviction (`evict`/`gopEnd`)
  protects P-frames *already in the queue* from being stranded without
  their own preceding I-frame when trimming retention — it does nothing
  when the very first frame pushed into an empty queue is itself a
  P-frame; there's no preceding I-frame to protect it with.
- This is a **documented, intentional simplification**, not an oversight:
  `docs/docs/archive/10-capture-policy.md` §5.1 states *"Первая
  реализация. Кадры отправляются в фконтейнер без всякой условной
  фильтрации — если сегмент открыт, каждый кадр идёт Заполнителю сразу же,
  как только получен."* The same doc (§4) separately mentions that under
  backpressure the system "снижает GOP или дожидается ключевого кадра"
  ("waits for a keyframe") when shedding frames — but that's aspirational
  for the backpressure path specifically, not implemented:
  `channelingest.go`'s `skipFrames` is a plain boolean toggle with no
  GOP/keyframe awareness.
- RTSP/RTP protocol itself doesn't guarantee the first packet after `PLAY`
  lands on a GOP boundary — some cameras start transmission at the next
  IDR as a QoS convenience, but this is camera-specific behavior, not
  something the protocol or this codebase currently enforces.

**Related, likely-same-root-cause gap found while investigating**:
`internal/playlist/segments.go`'s `buildRecordSegments` snaps every
**internal** HLS segment-cut boundary forward to the nearest keyframe
(`earliestAtOrAfter(keyframes, next)`), but the **first** segment boundary
of a record is hardcoded to `windowStart` with no such snapping (line
~65). So even independent of the ingest-side gap above, a record that
happens to start on a P-frame produces a first HLS segment that also
doesn't start on a keyframe — `mediacommon`'s `fmp4.PartSample.FillH264`/
`FillH265` will correctly mark that sample `IsNonSyncSample = true`, but
nothing seeks the segment's start forward to fix it.

No existing `.scratch` issue tracked this before now (checked all
`issues/*.md` for "keyframe"/"IDR"/"FrameKindI"/"first frame").

## Facts gathered during grilling

- `FrameQueue.Push` (the sole entry point into a channel's per-stream
  retention queue) is called *only* from the four RTSP decode callbacks in
  `internal/ingest/rtsp.go` — no other producer exists. A gate placed
  there is therefore sufficient by construction: it's structurally
  impossible for a "bare P-frame with no preceding I" to ever reach the
  queue, so every downstream consumer (GOP-atomic eviction, event
  policy's `Since`-based prerecord replay via `openSegmentLocked`) becomes
  safe automatically, with no separate event-policy-side fix needed.
- A channel's `streamQueue` is created once (lazily, on first `Push` for
  that stream) and never recreated — it persists across every reconnect.
  The "first frames are P before any I ever arrived" scenario is only
  live for a stream's first-ever connect, not ordinary reconnects (which
  append to an already GOP-aligned buffer) — but the gate still needs to
  re-arm on every reconnect regardless, since `rtsp.go`'s per-codec setup
  runs fresh each time and nothing guarantees the *new* session's first
  delivered AU is an IDR either.
- A mid-recording RTSP reconnect (continuous policy, already recording)
  today already leaves an untracked time gap in the fcontainer's frame
  sequence — `closeSegmentLocked` is never called from the reconnect path,
  and `Filler.AddFrames` just appends each frame with its own timestamp,
  no discontinuity marker. So "wait for keyframe on reconnect" only
  extends an already-silently-tolerated gap; it doesn't introduce a new
  failure mode.

## Design decisions (grilling, 2026-08-18)

- **Gate**: `internal/ingest/rtsp.go`'s `setupH264`/`setupH265` drop every
  decoded AU until the first one containing an IDR/random-access NALU,
  for both initial connect and every reconnect (the gate re-arms each
  time `run()`/`Setup()` executes fresh). Only after that first keyframe
  do frames reach `ci.policy.HandleFrame(...)`.
- **Video only**: the gate applies exclusively to the video stream(s).
  Audio has no keyframe concept (every audio frame is independently
  decodable) and is left ungated — audio may start a few frames earlier
  than video in a freshly (re)connected segment, which is harmless for
  VOD playback.
- **Policy-agnostic**: the gate lives below `CapturePolicy`, so it applies
  identically regardless of capture policy type (continuous/event) — no
  policy-specific branching.
- **Timeout**: if no keyframe arrives within 30–60s of `Play()`, log a
  warning (channel keeps waiting, no session abort) — covers the
  pathological "camera never sends an IDR" case, which would otherwise
  silently look like "connected but recording nothing."
- **Normal-case observability**: a debug-level log line reporting how many
  leading non-keyframe frames were dropped before the first keyframe
  arrived (routine case — should usually be a handful of frames at most).
- **Companion fix, same ticket**: `internal/playlist/segments.go`'s
  `buildRecordSegments` also snaps a record's *first* segment boundary
  forward to the nearest keyframe (`earliestAtOrAfter`), the same way it
  already does for internal cut boundaries — cheap defense-in-depth for
  fblocks recorded before this fix, and removes the asymmetry between the
  first boundary and every other one.
- **Backpressure/frame-shedding GOP-awareness is explicitly out of
  scope** — tracked separately, see
  `.scratch/capture-keyframe-start/issues/02-backpressure-gop-aware-shedding.md`.
- **No Plan Mode** — seams are already clear from this interview; straight
  to `/mattpocock-skills:tdd`.
- **Testing**: unit tests only, no new e2e scenario. Real RTSP/ffmpeg
  almost always delivers an IDR first for a fresh stream, so a live-camera
  e2e scenario is unlikely to reliably reproduce a P-frame-first stream
  (this bug was only caught by chance on a real camera). Test via
  synthetic decoded-AU sequences fed directly at the `internal/ingest`
  seam (P,P,I,P,... → assert nothing reaches `HandleFrame` before the I),
  plus a unit test for `buildRecordSegments`'s first-boundary snap.

## Fix

- `internal/ingest/rtsp.go`: new `keyframeGate` type — `allow(kind,
  channel, logf)` drops frames until the first `mediatree.FrameKindI`
  arrives, then logs (once) how many leading frames were dropped.
  `newKeyframeGate` also arms a `time.AfterFunc(keyframeWaitTimeout, ...)`
  (default 30s, a `var` so tests can shrink it) that logs a `WARNING` if
  no keyframe has arrived by the time it fires — the pathological-case
  signal from Q6. `seen` is an `atomic.Bool` (read from the timer's own
  goroutine, written from the packet-decode callback's goroutine);
  `dropped` stays a plain `int` (touched only from the callback goroutine).
  `setupH264`/`setupH265` each construct their own `gate :=
  newKeyframeGate(ci.channel, ci.logf)` right where they used to declare
  `seenKeyframe := false` — since both functions already run fresh per
  RTSP session (initial connect and every reconnect), the gate re-arms
  automatically, no extra plumbing needed. Audio (`setupG711`/`setupAAC`)
  is untouched, per Q5.
- `internal/playlist/segments.go`'s `buildRecordSegments`: the first
  boundary is now `earliestAtOrAfter(keyframes, windowStart)` (falling
  back to `windowStart` verbatim only if no keyframe exists at or after it
  at all — defensive, for pre-fix data), matching how every later boundary
  already snapped forward to a keyframe.

## Tests

TDD red→green, all seams from the grilling session:
- `internal/ingest/rtsp_keyframegate_test.go` (new):
  `TestChannelIngest_H264_DropsLeadingNonKeyframeFrames` and its H265
  mirror — two leading P-frames before a real IDR are dropped, only the
  IDR-onward frames (2 of them) land in the fcontainer, first one's kind
  is `I`. Both drive `setupH264`/`setupH265` directly via the existing
  `onPacketOnlySource` fake + a real gortsplib RTP encoder (same pattern
  as `rtsp_paramscompare_test.go`), with SPS/PPS advertised at the SDP
  level so a missing-params validation error can't produce a false-positive
  pass. `TestChannelIngest_H264_LogsCountOfDroppedLeadingFrames` asserts
  the "dropped 2 leading non-keyframe" log line.
  `TestChannelIngest_H264_WarnsIfNoKeyframeArrivesWithinTimeout` shrinks
  `keyframeWaitTimeout` to 10ms and asserts a "no keyframe" warning fires
  when only P-frames ever arrive.
- `internal/playlist/playlist_test.go`:
  `TestRecordSegments_FirstBoundarySnapsToNearestKeyframe` — a record
  whose first two frames are P and third is the real keyframe produces
  exactly one segment starting at that keyframe's time, not at
  `rec.Begin`.

`go build ./...`, `go test ./...` (full repo, all 32 packages green),
`go test ./internal/ingest/... -race` (clean, validates the
`atomic.Bool`/timer-goroutine interaction), and `golangci-lint run
./internal/ingest/... ./internal/playlist/...` all clean.
