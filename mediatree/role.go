package mediatree

import "fmt"

// Role is the open, domain-growing semantic tag of a tree node
// (docs/docs/archive/05-data-format.md §3). Codes are append-only: once a
// code is written to disk it is never renumbered or reused
// (docs/docs/archive/07-media-tree.md §3.1, protobuf-field-number
// discipline). Video and audio each have their OWN code for
// structurally-identical roles (e.g. two frame_time codes, 19 and 32) —
// callers filtering across both modalities must check both codes, never
// match by name.
type Role uint16

const (
	RoleRoot     Role = 0
	RoleChannels Role = 1
	RoleChannel  Role = 2 // value: channel number, uint32 (logical ceiling uint16, ADR-014)
	RoleStreams  Role = 3
	RoleStream   Role = 4 // value: stream number, uint32 (logical ceiling uint16)
	RoleVideo    Role = 5
	RoleAudio    Role = 6

	RoleConfigsVideo      Role = 7
	RoleConfigVideo       Role = 8 // value: timestamp of param change
	RoleDataVideo         Role = 9
	RoleCodecVideo        Role = 10 // value: uint8, see Codec* constants
	RoleParamVPS          Role = 11 // bytes, H.265 only
	RoleParamSPS          Role = 12 // bytes
	RoleParamPPS          Role = 13 // bytes
	RoleFramerate         Role = 14 // float64
	RoleCodecProfileVideo Role = 15 // bytes
	RoleFramesVideo       Role = 16
	RoleFrameVideo        Role = 17
	RoleFrameDataVideo    Role = 18 // bytes
	RoleFrameTimeVideo    Role = 19 // timestamp
	RoleFrameKind         Role = 20 // uint8, see FrameKind* constants (video only)

	RoleConfigsAudio      Role = 21
	RoleConfigAudio       Role = 22 // value: timestamp of param change
	RoleDataAudio         Role = 23
	RoleCodecAudio        Role = 24 // value: uint8, see Codec* constants
	RoleCodecProfileAudio Role = 25 // bytes
	RoleSampleRate        Role = 26 // uint32, Hz
	RoleChannelCount      Role = 27 // uint8
	RoleParamAudioConfig  Role = 28 // bytes, AAC ASC only
	RoleFramesAudio       Role = 29
	RoleFrameAudio        Role = 30
	RoleFrameDataAudio    Role = 31 // bytes
	RoleFrameTimeAudio    Role = 32 // timestamp

	// RoleWidth/RoleHeight are optional siblings of RoleParamSPS/RoleParamPPS
	// under data(video) -- the stream's resolution, computed from the SPS at
	// ingest time so internal/ingest can compare a video config across a
	// setup/reconnect boundary without redundant config nodes. uint32, not
	// uint16: this format has no uint16 primitive (same container-type
	// choice as RoleChannel/RoleStream).
	RoleWidth  Role = 33 // uint32
	RoleHeight Role = 34 // uint32
)

// roleNames is for diagnostics only — the numeric code, not this name, is
// the identity of a role (docs/docs/archive/07-media-tree.md §3.1: video and
// audio share names but never codes).
var roleNames = map[Role]string{
	RoleRoot: "root", RoleChannels: "channels", RoleChannel: "channel",
	RoleStreams: "streams", RoleStream: "stream", RoleVideo: "video", RoleAudio: "audio",
	RoleConfigsVideo: "configs(video)", RoleConfigVideo: "config(video)", RoleDataVideo: "data(video)",
	RoleCodecVideo: "codec(video)", RoleParamVPS: "param_vps", RoleParamSPS: "param_sps",
	RoleParamPPS: "param_pps", RoleFramerate: "framerate", RoleCodecProfileVideo: "codec_profile(video)",
	RoleFramesVideo: "frames(video)", RoleFrameVideo: "frame(video)", RoleFrameDataVideo: "frame_data(video)",
	RoleFrameTimeVideo: "frame_time(video)", RoleFrameKind: "frame_kind",
	RoleConfigsAudio: "configs(audio)", RoleConfigAudio: "config(audio)", RoleDataAudio: "data(audio)",
	RoleCodecAudio: "codec(audio)", RoleCodecProfileAudio: "codec_profile(audio)", RoleSampleRate: "sample_rate",
	RoleChannelCount: "channel_count", RoleParamAudioConfig: "param_audio_config",
	RoleFramesAudio: "frames(audio)", RoleFrameAudio: "frame(audio)", RoleFrameDataAudio: "frame_data(audio)",
	RoleFrameTimeAudio: "frame_time(audio)",
	RoleWidth:          "width", RoleHeight: "height",
}

func (r Role) String() string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	return fmt.Sprintf("role(%d)", uint16(r))
}

// CodecUninitialized is the shared zero value of both the video and audio
// codec enums below (RoleCodecVideo/RoleCodecAudio) — reserved so a Go
// zero-value uint8 codec field that was never explicitly set reads as
// "not set", not as a real codec (.scratch/codec-enum-values/issues/
// 01-codecs-start-at-one.md). Never a valid value to persist.
const CodecUninitialized uint8 = 0

// Video codec values for RoleCodecVideo (uint8), docs/docs/archive/
// 07-media-tree.md §3.1. Like Role codes (§3 above), append-only: once
// assigned here, a value is never renumbered or reused.
const (
	CodecH264 uint8 = 1
	CodecH265 uint8 = 2
)

// Audio codec values for RoleCodecAudio (uint8), docs/docs/archive/
// 07-media-tree.md §3.1. Like Role codes (§3 above), append-only: once
// assigned here, a value is never renumbered or reused.
const (
	CodecPCM   uint8 = 1
	CodecAAC   uint8 = 2
	CodecG711A uint8 = 3 // PCMA
	CodecG711U uint8 = 4 // PCMU — never observed in a real capture; assigned
	// by analogy with PCMA (same codec family, different companding law,
	// RFC 3551). Verify against the first real SDP offering PCMU.
)

// FrameKind values for RoleFrameKind (uint8) — the ASCII code of the letter,
// so the byte is self-describing in a hex dump (same convention as
// FARCPROL), docs/docs/archive/07-media-tree.md §3.1/§5.
const (
	FrameKindI uint8 = 0x49 // 'I' — start of a new GOP
	FrameKindP uint8 = 0x50 // 'P' — non-keyframe, same GOP
)

// FrameTimeRoles are the two role codes ("frame_time") that a cross-modality
// time query must filter by together — one name, two codes
// (docs/docs/archive/07-media-tree.md §3.1 note on code 19 vs 32).
var FrameTimeRoles = []Role{RoleFrameTimeVideo, RoleFrameTimeAudio}
