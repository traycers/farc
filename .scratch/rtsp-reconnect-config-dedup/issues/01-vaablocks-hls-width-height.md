# Surface persisted Width/Height in vaablocks/HLS

Status: fixed (2026-08-18, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — msm side only, see Scope decision

## Problem

As part of fixing the duplicate-`config(video)`-node bug (bogus `config`
nodes from cameras resending identical in-band SPS/PPS before every IDR
frame — see the sibling fix in `internal/ingest`), `fcontainer.StreamParams`
gained new optional `Width`/`Height` fields, persisted as new
`mediatree.RoleWidth`/`RoleHeight` nodes under `data(video)` (next to
`codec`/`param_sps`/`param_pps`). They exist purely so `internal/ingest` can
compare a video stream's real resolution across a setup/reconnect boundary
without redundant config nodes.

Both known downstream consumers of `data(video)`'s children already
tolerate unknown roles safely:

- `internal/vaablocks/streamconfig.go`'s `extractOneConfig` switches on
  known roles with an explicit `//nolint:exhaustive` no-op for anything
  else — `RoleWidth`/`RoleHeight` are invisible to it today.
- `internal/segment/init.go` (`resolveVideoConfig`) and
  `internal/playlist/config.go` (`videoSigFor`) both use
  `toc.ScanByRole` with an explicit allowlist (`RoleCodecVideo`,
  `RoleParamSPS`, `RoleParamPPS`) — same story.

So nothing is broken by the new fields existing, but the resolution data
itself is currently write-only: nothing reads it back out.

## Why this matters

msm/controller (`internal/msmd`'s `params_add`, per
`temp/msm/openapi.yaml`) and/or the HLS player path may want to know a
channel's video resolution without having to parse SPS themselves. Since
the bytes are already being computed and persisted for the dedup fix, this
is now cheap to expose if there's an actual external consumer that wants
it.

## Scope decision (grilling)

- Do it now, msm side only: `vaablocks.StreamConfig` gains `Width`/
  `Height`, reported via `params_add`. HLS side (CMAF `tkhd`/`stsd` boxes)
  stays out of scope — no consumer scenario exists there even
  hypothetically (browsers get real resolution from the decoded video
  itself), so adding code for it would be pure speculation.

## Fix

`internal/vaablocks/streamconfig.go`: `StreamConfig` gained `Width`/
`Height uint32` fields (0 means absent, mirroring
`fcontainer.StreamParams`' own convention); `extractOneConfig`'s role
switch gained `mediatree.RoleWidth`/`RoleHeight` cases.

`internal/msmd/params.go`'s `resolvedConfig.data()`: emits `"width"`/
`"height"` in the video `params_add` JSON payload when non-zero (same
omit-if-absent pattern as `sps`/`pps`/`vps`/`framerate`).

**Tests**: `internal/vaablocks/vaablocks_test.go`'s
`TestStreamConfigs_VideoWidthHeight` (a config with `Width: 1920, Height:
1080` round-trips through `StreamConfigs`). `internal/msmd/msmd_test.go`'s
`writeChannelVideo` helper now sets `Width: 1920, Height: 1080`, and
`TestHandleFblockReady_FullFlow` asserts the `params_add` payload carries
both. See the sibling work in `internal/ingest`/`internal/fcontainer`/
`mediatree` (config-node dedup + RTSP reconnect, 2026-08-14) for where
`Width`/`Height` originally get written.
