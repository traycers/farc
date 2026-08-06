package segment

import (
	"context"
	"fmt"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/tocindex"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// frameMeta is one frame's timing and location, resolved from the TOC
// without touching Content.
type frameMeta struct {
	Time      uint64
	DataRange hlsclient.Range
	Sync      bool // true for a video keyframe, or always true for audio
}

// gatherFrames collects, in ascending time order, every frame under a
// (dataRole, timeRole, kindRole) triple within [start,stop) whose time falls
// in this segment's range. Each segment owns its own starting frame ([segStart,
// inclusive) up to, but not including, segEnd — except the record's very
// last segment (segEnd >= the record's own last frame time), which also
// owns the trailing frame at exactly segEnd, since no later segment exists
// to claim it. kindRole == 0 means "no kind concept" (audio): every frame is
// treated as a sync sample.
func gatherFrames(c *toc.Columns, start, stop uint32, timeRole, dataRole, kindRole mediatree.Role, segStart, segEnd uint64, last bool) []frameMeta {
	var out []frameMeta
	for _, timeID := range toc.InRange(toc.ScanByRole(c, timeRole), start, stop) {
		t := c.ValueOrOffset[timeID]
		if t < segStart {
			continue
		}
		if last {
			if t > segEnd {
				continue
			}
		} else if t >= segEnd {
			continue
		}

		frameID := c.Parent[timeID]
		dataID, ok := findChildByRole(c, frameID, dataRole)
		if !ok {
			continue
		}
		offset, size, ok := toc.ContentOffset(c, dataID)
		if !ok {
			continue
		}

		sync := kindRole == 0
		if !sync {
			if kindID, ok := findChildByRole(c, frameID, kindRole); ok {
				if v, ok := toc.InlineValue(c, kindID); ok && len(v) == 1 {
					sync = v[0] == mediatree.FrameKindI
				}
			}
		}
		out = append(out, frameMeta{Time: t, DataRange: hlsclient.Range{Offset: offset, Size: size}, Sync: sync})
	}
	return out
}

// sampleDuration returns frames[i]'s duration: the gap to the next frame, or
// to windowEnd for the last frame in the slice (so sample durations always
// sum to exactly the segment's own duration).
func sampleDuration(frames []frameMeta, i int, windowEnd uint64) uint32 {
	var end uint64
	if i+1 < len(frames) {
		end = frames[i+1].Time
	} else {
		end = windowEnd
	}
	return uint32(end - frames[i].Time)
}

// BuildMedia builds one CMAF media segment (seg.m4s) covering
// [segStart, segEnd) of rec's channel (ADR-019: entirely within this one
// fcontainer). Whether this is the record's trailing segment — and
// therefore whether the frame at exactly segEnd belongs to it — is derived
// from rec.End rather than a separate flag, since
// internal/playlist.RecordSegments always computes its grid over the
// record's own full [Begin,End] and always terminates it at exactly
// rec.End, regardless of which playback query originally asked for this
// segment (that query-independence is what lets internal/hlsapi's segment
// route recompute segStart/segEnd for a given segIndex from rec alone).
func BuildMedia(ctx context.Context, client *hlsclient.Client, rec tocindex.Record, channel uint16, segStart, segEnd uint64) ([]byte, error) {
	start, stop, ok := channelSubtree(rec.Columns, channel)
	if !ok {
		return nil, fmt.Errorf("segment: channel %d not present in fcontainer %x", channel, rec.UUID)
	}
	last := segEnd >= rec.End

	videoFrames := gatherFrames(rec.Columns, start, stop, mediatree.RoleFrameTimeVideo, mediatree.RoleFrameDataVideo, mediatree.RoleFrameKind, segStart, segEnd, last)
	audioFrames := gatherFrames(rec.Columns, start, stop, mediatree.RoleFrameTimeAudio, mediatree.RoleFrameDataAudio, 0, segStart, segEnd, last)
	if len(videoFrames) == 0 && len(audioFrames) == 0 {
		return nil, fmt.Errorf("segment: no frames in [%d,%d) for channel %d in fcontainer %x", segStart, segEnd, channel, rec.UUID)
	}

	ranges := make([]hlsclient.Range, 0, len(videoFrames)+len(audioFrames))
	for _, f := range videoFrames {
		ranges = append(ranges, f.DataRange)
	}
	for _, f := range audioFrames {
		ranges = append(ranges, f.DataRange)
	}
	bufs, err := client.ReadRanges(ctx, rec.StorageID, rec.UUID, ranges)
	if err != nil {
		return nil, fmt.Errorf("segment: read frame data: %w", err)
	}

	var tracks []*fmp4.PartTrack
	pos := 0
	if len(videoFrames) > 0 {
		samples := make([]*fmp4.Sample, len(videoFrames))
		for i, f := range videoFrames {
			var au h264.AnnexB
			err = au.Unmarshal(bufs[pos])
			if err != nil {
				return nil, fmt.Errorf("segment: parse Annex-B video frame at t=%d: %w", f.Time, err)
			}
			sample := &fmp4.Sample{}
			err = sample.FillH264(0, au)
			if err != nil {
				return nil, fmt.Errorf("segment: encode AVCC video frame at t=%d: %w", f.Time, err)
			}
			sample.Duration = sampleDuration(videoFrames, i, segEnd)
			samples[i] = sample
			pos++
		}
		tracks = append(tracks, &fmp4.PartTrack{ID: videoTrackID, Samples: samples})
	}
	if len(audioFrames) > 0 {
		samples := make([]*fmp4.Sample, len(audioFrames))
		for i := range audioFrames {
			samples[i] = &fmp4.Sample{Payload: bufs[pos], Duration: sampleDuration(audioFrames, i, segEnd)}
			pos++
		}
		tracks = append(tracks, &fmp4.PartTrack{ID: audioTrackID, Samples: samples})
	}

	part := fmp4.Part{SequenceNumber: 1, Tracks: tracks}
	var buf seekablebuffer.Buffer
	err = part.Marshal(&buf)
	if err != nil {
		return nil, fmt.Errorf("segment: marshal media segment: %w", err)
	}
	return buf.Bytes(), nil
}
