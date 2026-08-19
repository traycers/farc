// Package segment muxes one fcontainer's frames into CMAF (fMP4) init and
// media segments for HLS delivery, using
// github.com/bluenviron/mediacommon/v2's fmp4 writer. Per the earlier
// design discussion, only H.264 video and AAC audio are supported (pure
// remux, no transcoding); everything else is a hard error at build time.
//
// Frame bytes are fetched on demand via hlsclient.ReadRanges, using offsets
// already known from the fcontainer's own TOC (tocindex.Record.Columns) --
// no farcd-side resolve call is needed per segment, which is the entire
// point of ADR-018's push-built index.
package segment

import (
	"context"
	"fmt"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// timeScale is used for both video and audio tracks: farc's own frame
// timestamps are already Unix ns, so declaring the fMP4 timescale as
// 1e9 ticks/second lets every sample Duration be used directly with zero
// resampling, at the cost of diverging from the conventional
// framerate/sample-rate-based timescale most muxers pick. Players only ever
// consume mdhd's declared timescale, so this is transparent to output.
const timeScale = 1_000_000_000

const (
	videoTrackID = 1
	audioTrackID = 2
)

// videoConfig is one channel's active H.264 parameter set within a record,
// resolved once and shared by BuildInit and BuildMedia's callers.
type videoConfig struct {
	codec    uint8
	sps, pps hlsclient.Range
	hasVideo bool
}

type audioConfig struct {
	codec    uint8
	asc      hlsclient.Range
	hasAudio bool
}

// resolveVideoConfig finds channel's most recent (last, by node-id/creation
// order) video codec/SPS/PPS within [start,stop). ok is false if the
// channel carries no video config at all in this record.
func resolveVideoConfig(c *toc.Columns, start, stop uint32) (cfg videoConfig, ok bool) {
	codecIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleCodecVideo), start, stop)
	spsIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamSPS), start, stop)
	ppsIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamPPS), start, stop)
	if len(codecIDs) == 0 || len(spsIDs) == 0 || len(ppsIDs) == 0 {
		return videoConfig{}, false
	}
	if v, ok := toc.InlineValue(c, codecIDs[len(codecIDs)-1]); ok && len(v) >= 1 {
		cfg.codec = v[0]
	}
	spsOff, spsSize, _ := toc.ContentOffset(c, spsIDs[len(spsIDs)-1])
	ppsOff, ppsSize, _ := toc.ContentOffset(c, ppsIDs[len(ppsIDs)-1])
	cfg.sps = hlsclient.Range{Offset: spsOff, Size: spsSize}
	cfg.pps = hlsclient.Range{Offset: ppsOff, Size: ppsSize}
	cfg.hasVideo = true
	return cfg, true
}

// resolveAudioConfig is resolveVideoConfig's audio (AAC ASC) counterpart.
func resolveAudioConfig(c *toc.Columns, start, stop uint32) (cfg audioConfig, ok bool) {
	codecIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleCodecAudio), start, stop)
	ascIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamAudioConfig), start, stop)
	if len(codecIDs) == 0 || len(ascIDs) == 0 {
		return audioConfig{}, false
	}
	if v, ok := toc.InlineValue(c, codecIDs[len(codecIDs)-1]); ok && len(v) >= 1 {
		cfg.codec = v[0]
	}
	ascOff, ascSize, _ := toc.ContentOffset(c, ascIDs[len(ascIDs)-1])
	cfg.asc = hlsclient.Range{Offset: ascOff, Size: ascSize}
	cfg.hasAudio = true
	return cfg, true
}

// BuildInit builds the CMAF init segment (init.mp4) for rec's channel: one
// track per supported codec actually present (video, audio, or both).
func BuildInit(ctx context.Context, client hlsclient.API, rec tocindex.Record, channel uint16) ([]byte, error) {
	start, stop, ok := toc.ChannelSubtreeRange(rec.Columns, channel)
	if !ok {
		return nil, fmt.Errorf("segment: channel %d not present in fcontainer %x", channel, rec.UUID)
	}

	video, hasVideo := resolveVideoConfig(rec.Columns, start, stop)
	audio, hasAudio := resolveAudioConfig(rec.Columns, start, stop)
	if !hasVideo && !hasAudio {
		return nil, fmt.Errorf("segment: no supported (H.264/AAC) tracks for channel %d in fcontainer %x", channel, rec.UUID)
	}
	if hasVideo && video.codec != mediatree.CodecH264 {
		return nil, fmt.Errorf("segment: unsupported video codec %d for channel %d (only H.264)", video.codec, channel)
	}
	if hasAudio && audio.codec != mediatree.CodecAAC {
		return nil, fmt.Errorf("segment: unsupported audio codec %d for channel %d (only AAC)", audio.codec, channel)
	}

	var ranges []hlsclient.Range
	if hasVideo {
		ranges = append(ranges, video.sps, video.pps)
	}
	if hasAudio {
		ranges = append(ranges, audio.asc)
	}
	bufs, err := client.ReadRanges(ctx, rec.StorageID, rec.UUID, ranges)
	if err != nil {
		return nil, fmt.Errorf("segment: read config bytes: %w", err)
	}

	var tracks []*fmp4.InitTrack
	pos := 0
	if hasVideo {
		sps, pps := bufs[pos], bufs[pos+1]
		pos += 2
		tracks = append(tracks, &fmp4.InitTrack{ID: videoTrackID, TimeScale: timeScale, Codec: &codecs.H264{SPS: sps, PPS: pps}})
	}
	if hasAudio {
		asc := bufs[pos]
		var ascCfg mpeg4audio.AudioSpecificConfig
		err = ascCfg.Unmarshal(asc)
		if err != nil {
			return nil, fmt.Errorf("segment: parse AAC AudioSpecificConfig: %w", err)
		}
		tracks = append(tracks, &fmp4.InitTrack{ID: audioTrackID, TimeScale: timeScale, Codec: &codecs.MPEG4Audio{Config: ascCfg}})
	}

	init := fmp4.Init{Tracks: tracks}
	var buf seekablebuffer.Buffer
	err = init.Marshal(&buf)
	if err != nil {
		return nil, fmt.Errorf("segment: marshal init: %w", err)
	}
	return buf.Bytes(), nil
}
