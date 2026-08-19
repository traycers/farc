# Audio VAA-blocks for msm integration

Status: fixed (2026-08-18, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

`internal/vaablocks`/`internal/msmd` currently compute and report VAA-blocks
(`vaa_blocks_add`) for video streams only — see `internal/msmd/msmd.go`'s
`reportChannel`, whose own doc comment says stream params are reported for
"video and audio alike ... even though only video ever becomes a
vaa-block", and CLAUDE.md's description of `internal/vaablocks` as
computing "per-channel video-only 'vaa-blocks'".

During domain modeling (2026-08-12) it came up that the actual integration
need is for **audio VAA-blocks to be sent too**, not just video. Confirmed
directly by the user: "params_add покрывает и видео, и аудио и будущие
типы потоков. Для интеграции надо бы слать ваа-блоки со звуком."

## Why this matters

`params_add` already reports stream config for every stream type
(video/audio/future types) on a channel, so the metadata side is already
type-agnostic. The gap is specifically in `vaablocks.Compute`, which only
walks video columns, and in `reportChannel`, which only calls
`VaaBlocksAdd` for the video-derived blocks.

## Investigation

`StreamConfigs` was already kind-agnostic (reports both video and audio
configs for `params_add`). `Compute` was the only video-only piece,
hardcoded to `mediatree.RoleFrameTimeVideo` and a parent-chain walk named
for video. The "≥2s gap"/"never cross a fblock" rule turned out to have no
video-specific rationale anywhere (it's not from ADR-019, which is
actually about the unrelated HLS-segment/fcontainer boundary) — it's just
`vaablocks.go`'s own generic rule, coincidentally only ever applied to
video so far. `msmd.reportChannel` called `Compute` once per channel and
hardcoded `StreamType: streamTypeVideo` on every `VaaBlocksAdd`. The msm
wire contract (`temp/msm/openapi.yaml`'s `vaa_blocks_add`) already accepts
`stream_type` 1 (video) or 2 (audio) — no contract change needed.

## Design decisions (grilling)

- Video and audio get **independent** vaa-block timelines per channel —
  no cross-kind gap synchronization.
- `Compute` gained a `kind StreamKind` parameter (not a separate
  `ComputeAudio`) — the gap-splitting algorithm is identical for both
  kinds, only the role codes it scans/walks differ.
- The fblock-boundary rule needed no design decision — `Compute` only
  ever sees one fblock's TOC, structurally, regardless of kind.

## Fix

`internal/vaablocks/vaablocks.go`: `Compute(c, channel, kind StreamKind)`;
`videoFrameSpan`/`configAndStreamOf` generalized to `frameSpan`/
`configAndStreamOf` with new `frameTimeRole`/`frameDataRole`/`kindRole`
helpers dispatching video/audio role codes by `kind` (mirroring
`streamconfig.go`'s existing dispatch style).

`internal/msmd/msmd.go`'s `reportChannel`: loops `{KindVideo,
streamTypeVideo}, {KindAudio, streamTypeAudio}`, calling `Compute` and
`VaaBlocksAdd` once per kind (both before that fblock's `InfoSet`, per
msm's own ordering requirement).

**Tests**: `internal/vaablocks/vaablocks_test.go` gained
`writeChannelAudio` + `TestCompute_Audio_SplitsOnGap` (mirrors the
existing video gap-split test); all pre-existing `Compute` call sites
updated to the new 3-arg signature. `internal/msmd/msmd_test.go` gained
`writeChannelVideoAndAudio` +
`TestHandleFblockReady_ReportsBothVideoAndAudioVaaBlocks`, asserting both
`VaaBlocksAdd` calls happen (one per `StreamType`) before `InfoSet`.
