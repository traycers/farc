package vaablocks

import (
	"encoding/binary"
	"fmt"
	"math"

	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// StreamKind mirrors internal/fcontainer.StreamKind (not imported --
// internal/fcontainer is farcd's own write-path package, and this package
// only ever sees the TOC after the fact).
type StreamKind int

const (
	KindVideo StreamKind = iota
	KindAudio
)

// BytesRef is a byte range in the fblock's Content section (the same frame
// of reference as toc.ContentOffset / GET .../fcontainers/{uuid}?ranges=).
// StreamConfig reports blob-valued fields this way rather than resolved
// bytes, because the WS "toc" push (internal/api/eventpush.go) only carries
// the TOC section, not Content -- resolving a BytesRef into actual bytes is
// the caller's job (internal/msmd fetches the ones it needs over farcd's
// HTTP content-range API).
type BytesRef struct {
	Offset uint64
	Size   uint64
}

// StreamConfig is one config version's codec parameters, extracted from a
// fblock's TOC for msm's params_add -- see the video/audio params-format
// docs (video.schema.json/audio.schema.json) for the JSON shape the
// resolved fields eventually feed into. A Has* flag false (or a BytesRef
// with Size 0) means that field is absent -- msm's schemas allow that for
// every field except Codec/SampleRate/ChannelCount.
type StreamConfig struct {
	ConfigID uint32 // matches Block.ConfigID for the vaa-blocks that used it
	StreamID uint16
	Kind     StreamKind
	Time     uint64 // ns, when this config version started being used

	Codec uint8 // mediatree.CodecH264/H265 or CodecPCM/AAC/G711A/G711U

	HasProfile bool
	Profile    BytesRef // ASCII bytes, e.g. "Main"/"High"/"LC"

	// Video only.
	HasFramerate bool
	Framerate    float64
	HasSPS       bool
	SPS          BytesRef
	HasPPS       bool
	PPS          BytesRef
	HasVPS       bool
	VPS          BytesRef

	// Audio only.
	SampleRate     uint32
	ChannelCount   uint8
	HasAudioConfig bool
	AudioConfig    BytesRef
}

// StreamConfigs returns every config version present under channel's
// subtree in c (one entry per params-change event a live capture actually
// recorded — usually exactly one per stream/kind), in TOC row-id order.
// nil if channel isn't present in c.
func StreamConfigs(c *toc.Columns, channel uint16) ([]StreamConfig, error) {
	channelNodeID, ok := findChannelNode(c, channel)
	if !ok {
		return nil, nil
	}
	streamsID, ok := findChildByRole(c, channelNodeID, mediatree.RoleStreams)
	if !ok {
		return nil, nil
	}
	_, streamsEnd := toc.SubtreeRange(c, streamsID)

	var out []StreamConfig
	for i := streamsID + 1; i < streamsEnd; i++ {
		if c.Parent[i] != streamsID || c.Role[i] != mediatree.RoleStream {
			continue
		}
		v, ok := toc.InlineValue(c, i)
		if !ok || len(v) != 4 {
			continue
		}
		streamID := uint16(binary.LittleEndian.Uint32(v))

		if videoID, ok := findChildByRole(c, i, mediatree.RoleVideo); ok {
			cfgs, err := extractConfigs(c, videoID, streamID, KindVideo)
			if err != nil {
				return nil, err
			}
			out = append(out, cfgs...)
		}
		if audioID, ok := findChildByRole(c, i, mediatree.RoleAudio); ok {
			cfgs, err := extractConfigs(c, audioID, streamID, KindAudio)
			if err != nil {
				return nil, err
			}
			out = append(out, cfgs...)
		}
	}
	return out, nil
}

func extractConfigs(c *toc.Columns, kindID uint32, streamID uint16, kind StreamKind) ([]StreamConfig, error) {
	configsRole, configRole := mediatree.RoleConfigsVideo, mediatree.RoleConfigVideo
	if kind == KindAudio {
		configsRole, configRole = mediatree.RoleConfigsAudio, mediatree.RoleConfigAudio
	}
	configsID, ok := findChildByRole(c, kindID, configsRole)
	if !ok {
		return nil, nil
	}
	_, configsEnd := toc.SubtreeRange(c, configsID)

	var out []StreamConfig
	for i := configsID + 1; i < configsEnd; i++ {
		if c.Parent[i] != configsID || c.Role[i] != configRole {
			continue
		}
		sc, err := extractOneConfig(c, i, streamID, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

func extractOneConfig(c *toc.Columns, configID uint32, streamID uint16, kind StreamKind) (StreamConfig, error) {
	sc := StreamConfig{ConfigID: configID, StreamID: streamID, Kind: kind, Time: c.ValueOrOffset[configID]}

	dataRole := mediatree.RoleDataVideo
	if kind == KindAudio {
		dataRole = mediatree.RoleDataAudio
	}
	dataID, ok := findChildByRole(c, configID, dataRole)
	if !ok {
		return sc, fmt.Errorf("vaablocks: config %d has no data child", configID)
	}
	_, dataEnd := toc.SubtreeRange(c, dataID)
	for i := dataID + 1; i < dataEnd; i++ {
		if c.Parent[i] != dataID {
			continue
		}
		switch c.Role[i] { //nolint:exhaustive // only the codec/profile/param roles below carry information this loop needs; every other role under a data(...) node is a deliberate no-op
		case mediatree.RoleCodecVideo, mediatree.RoleCodecAudio:
			if v, ok := toc.InlineValue(c, i); ok && len(v) == 1 {
				sc.Codec = v[0]
			}
		case mediatree.RoleCodecProfileVideo, mediatree.RoleCodecProfileAudio:
			if off, size, ok := toc.ContentOffset(c, i); ok {
				sc.HasProfile, sc.Profile = true, BytesRef{off, size}
			}
		case mediatree.RoleFramerate:
			if v, ok := toc.InlineValue(c, i); ok && len(v) == 8 {
				sc.HasFramerate = true
				sc.Framerate = math.Float64frombits(binary.LittleEndian.Uint64(v))
			}
		case mediatree.RoleParamSPS:
			if off, size, ok := toc.ContentOffset(c, i); ok {
				sc.HasSPS, sc.SPS = true, BytesRef{off, size}
			}
		case mediatree.RoleParamPPS:
			if off, size, ok := toc.ContentOffset(c, i); ok {
				sc.HasPPS, sc.PPS = true, BytesRef{off, size}
			}
		case mediatree.RoleParamVPS:
			if off, size, ok := toc.ContentOffset(c, i); ok {
				sc.HasVPS, sc.VPS = true, BytesRef{off, size}
			}
		case mediatree.RoleSampleRate:
			if v, ok := toc.InlineValue(c, i); ok && len(v) == 4 {
				sc.SampleRate = binary.LittleEndian.Uint32(v)
			}
		case mediatree.RoleChannelCount:
			if v, ok := toc.InlineValue(c, i); ok && len(v) == 1 {
				sc.ChannelCount = v[0]
			}
		case mediatree.RoleParamAudioConfig:
			if off, size, ok := toc.ContentOffset(c, i); ok {
				sc.HasAudioConfig, sc.AudioConfig = true, BytesRef{off, size}
			}
		}
	}
	return sc, nil
}
