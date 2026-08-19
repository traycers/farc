# One RTSP channel should produce one stream containing both video and audio, not two separate streams

Status: fixed (2026-08-18, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

Reported 2026-08-17, from a tree copied into the (since-reverted) repo-root
`TODO.md`. For a single RTSP channel with both a video and an audio track,
the fblock tree currently shows two separate `stream` nodes under that
channel:

```
channel (uint32) = 1
└── streams (void)
    ├── stream (uint32) = 0
    │   └── video (void) ...
    └── stream (uint32) = 1
        └── audio (void) ...
```

Per the user, this is wrong: one RTSP source (one channel) should produce
**one** stream containing both its video and audio, not two independently-
numbered streams that happen to share a channel number.

## Investigation

Grilling session traced the bug to a single spot: `internal/ingest`'s
per-track `streamNum`, incremented once per RTSP media in the loop over
`desc.Medias` inside `ChannelIngest.run` — video got `streamNum=0`, audio
`streamNum=1`, so they landed in different `stream` tree nodes.

`internal/fcontainer.Filler` was never the problem: `getOrCreateKindBranch`
already supports hanging both a `video` and an `audio` child off the same
`stream` node, and `docs/docs/archive/07-media-tree.md` already documents
exactly that shape (a `stream` has 0..1 `video` and 0..1 `audio` children).
No downstream consumer (`internal/vaablocks`, `internal/segment/init.go`,
`internal/playlist/config.go`, the tree API/web component) indexes by
stream number — all scan by role — so none needed changes.

## Design decisions (grilling)

- **Backward compatibility**: none needed — first version, free to change
  the write-path output; old fblocks keep their old two-stream shape
  forever (fblocks are immutable), no migration code required.
- **Stream numbering model**: a channel maps to exactly one RTSP link
  today ("streams" plural is reserved for a future multi-link-per-channel
  case, not implemented) — so every track from one `Describe()` call
  shares `streamNum = 0`, replacing the per-track increment entirely.
- **msm-facing consequence**: video and audio of one channel now report
  the same `StreamID` via `params_add`/`vaa_blocks_add` (previously
  different). Accepted — msm/controller differentiates by `stream id +
  stream type`, not by numeric stream id alone.
- **Duplicate-track edge case**: if one RTSP link's SDP ever described two
  tracks of the same kind (not seen from real cameras, no explicit
  prohibition in gortsplib/the code), the second is logged and ignored,
  not merged/overwritten and not a hard error.

## Fix

`internal/ingest/channelingest.go`'s `run`: replaced the incrementing
`streamNum` with a `const streamNum uint32 = 0` shared by every media on
the link, plus a `seenKind map[fcontainer.StreamKind]bool` threaded
through to `setupMedia`.

`internal/ingest/rtsp.go`'s `setupMedia`: gained a `seenKind` parameter and
a `claim(kind, formatName) bool` closure — logs and skips a format whose
kind is already claimed for this link instead of calling into
`setupH264`/`setupH265`/`setupG711`/`setupAAC` (which would otherwise
silently overwrite the first track's cached params via
`reportVideoParams`/`reportAudioParams`).

**Tests** (`internal/ingest/channelingest_test.go`, TDD via
`/mattpocock-skills:tdd`):
- `TestChannelIngest_RunPutsVideoAndAudioUnderTheSameStreamNode`: drives a
  real `ChannelIngest.run` session (via a new `dualMediaSource` fake that
  records per-media `OnPacketRTP` callbacks) with one H264 + one G711
  media, sends one frame of each, and asserts exactly one `RoleStream`
  node exists with both a `video` and an `audio` child. Red before the
  fix (asserted 2 stream nodes), green after.
- `TestChannelIngest_SetupMediaIgnoresSecondTrackOfSameKindOnOneLink`:
  calls `setupMedia` twice with two H264 medias sharing one `seenKind`
  map, asserts the cached video params are the first track's (not
  overwritten by the second) and that a log line was emitted.
