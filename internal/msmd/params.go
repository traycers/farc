package msmd

import (
	"context"
	"encoding/base64"
	"fmt"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/vaablocks"
	"traycers/farc/mediatree"
)

// paramsKey identifies one already-reported config version. sc.Time (the ns
// timestamp CapturePolicy.ensureConfigLocked stamped this config with when
// it first saw it -- internal/ingest/policy.go) is a sufficient dedup key on
// its own: it's carried forward unchanged into every later fblock's TOC for
// as long as the (channel, stream, kind)'s actual codec params stay the
// same (ensureConfigLocked only mints a new config -- and therefore a new
// Time -- on a genuine change), so two configs with equal (channel,
// streamID, kind, time) are guaranteed to be byte-for-byte identical without
// this package ever having to fetch and compare their SPS/PPS/VPS bytes
// itself.
type paramsKey struct {
	aid      string
	channel  uint16
	streamID uint16
	kind     vaablocks.StreamKind
	time     uint64
}

// ensureParams returns config's params_id, params_add'ing it (after
// resolving its blob fields over HTTP) the first time this exact
// (aid, channel, streamID, kind, time) combination is seen.
func (p *processor) ensureParams(ctx context.Context, aid string, channel uint16, fblockID [16]byte, config vaablocks.StreamConfig) (int64, error) {
	key := paramsKey{aid: aid, channel: channel, streamID: config.StreamID, kind: config.Kind, time: config.Time}
	if id, ok := p.paramsSeen[key]; ok {
		return id, nil
	}

	resolved, err := p.resolveConfig(ctx, aid, fblockID, config)
	if err != nil {
		return 0, err
	}

	p.nextParamsID++
	id := p.nextParamsID
	streamType := streamTypeVideo
	if config.Kind == vaablocks.KindAudio {
		streamType = streamTypeAudio
	}
	err = p.out.ParamsAdd(ctx, aid, id, streamType, resolved.data())
	if err != nil {
		return 0, err
	}
	p.paramsSeen[key] = id
	return id, nil
}

// resolvedConfig is a StreamConfig with its blob-valued fields (profile,
// sps/pps/vps, audio config) fetched into actual bytes.
type resolvedConfig struct {
	sc                                  vaablocks.StreamConfig
	profile, sps, pps, vps, audioConfig []byte
}

// resolveConfig fetches every blob field sc actually has in one batched
// GET .../fcontainers/{uuid}?ranges=... call (ADR-004's ranged-read
// request) -- not one call per field.
func (p *processor) resolveConfig(ctx context.Context, storageID string, uuid [16]byte, sc vaablocks.StreamConfig) (resolvedConfig, error) {
	rc := resolvedConfig{sc: sc}

	var ranges []hlsclient.Range
	var dests []*[]byte
	add := func(has bool, ref vaablocks.BytesRef, dst *[]byte) {
		if has && ref.Size > 0 {
			ranges = append(ranges, hlsclient.Range{Offset: ref.Offset, Size: ref.Size})
			dests = append(dests, dst)
		}
	}
	add(sc.HasProfile, sc.Profile, &rc.profile)
	add(sc.HasSPS, sc.SPS, &rc.sps)
	add(sc.HasPPS, sc.PPS, &rc.pps)
	add(sc.HasVPS, sc.VPS, &rc.vps)
	add(sc.HasAudioConfig, sc.AudioConfig, &rc.audioConfig)
	if len(ranges) == 0 {
		return rc, nil
	}

	bufs, err := p.content.ReadRanges(ctx, storageID, uuid, ranges)
	if err != nil {
		return resolvedConfig{}, fmt.Errorf("read content ranges: %w", err)
	}
	if len(bufs) != len(dests) {
		return resolvedConfig{}, fmt.Errorf("read content ranges: got %d buffers, want %d", len(bufs), len(dests))
	}
	for i, buf := range bufs {
		*dests[i] = buf
	}
	return rc, nil
}

// data builds params_add's `data` payload, matching the video/audio
// params-format schemas (video.schema.json/audio.schema.json): a plain
// JSON object with codec/profile plus whichever fields this config
// actually carries -- fields the source never reported (HasX false, or an
// empty resolved blob) are simply omitted, exactly as those schemas
// document ("могут отсутствовать").
func (rc resolvedConfig) data() map[string]any {
	if rc.sc.Kind == vaablocks.KindAudio {
		v := map[string]any{
			"codec":          audioCodecName(rc.sc.Codec),
			"sample_rate":    rc.sc.SampleRate,
			"channels_count": rc.sc.ChannelCount,
		}
		if len(rc.profile) > 0 {
			v["profile"] = string(rc.profile)
		}
		if len(rc.audioConfig) > 0 {
			v["config"] = base64.StdEncoding.EncodeToString(rc.audioConfig)
		}
		return v
	}

	v := map[string]any{"codec": videoCodecName(rc.sc.Codec)}
	if len(rc.profile) > 0 {
		v["profile"] = string(rc.profile)
	}
	if rc.sc.HasFramerate {
		v["framerate"] = rc.sc.Framerate
	}
	if len(rc.vps) > 0 {
		v["vps"] = base64.StdEncoding.EncodeToString(rc.vps)
	}
	if len(rc.sps) > 0 {
		v["sps"] = base64.StdEncoding.EncodeToString(rc.sps)
	}
	if len(rc.pps) > 0 {
		v["pps"] = base64.StdEncoding.EncodeToString(rc.pps)
	}
	return v
}

func videoCodecName(c uint8) string {
	switch c {
	case mediatree.CodecH264:
		return "h264"
	case mediatree.CodecH265:
		return "h265"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}

func audioCodecName(c uint8) string {
	switch c {
	case mediatree.CodecPCM:
		return "pcm"
	case mediatree.CodecAAC:
		return "aac"
	case mediatree.CodecG711A:
		return "pcma"
	case mediatree.CodecG711U:
		return "pcmu"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}
