package tocindex

import (
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// gapThresholdNS is the gap (in ns) above which two consecutive video frames
// are considered separate presence segments rather than one continuous run.
const gapThresholdNS = uint64(2_000_000_000)

// Segment is one continuous run of a channel's video frames, split wherever
// the gap to the next frame is >= gapThresholdNS.
type Segment struct {
	Begin uint64
	End   uint64
}

// VideoPresenceSegments returns channel's video-presence segments from c, in
// increasing-time order -- nil if channel isn't present in c or has no video
// frames. Only needs frame timestamps: it exists purely to answer "did this
// channel have video at time t", so it skips any byte-offset/config/stream
// resolution entirely.
func VideoPresenceSegments(c *toc.Columns, channel uint16) []Segment {
	start, end, ok := toc.ChannelSubtreeRange(c, channel)
	if !ok {
		return nil
	}
	timeIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleFrameTimeVideo), start, end)
	if len(timeIDs) == 0 {
		return nil
	}

	var segments []Segment
	runStart := 0
	for i := 1; i <= len(timeIDs); i++ {
		if i < len(timeIDs) && c.ValueOrOffset[timeIDs[i]]-c.ValueOrOffset[timeIDs[i-1]] < gapThresholdNS {
			continue
		}
		segments = append(segments, Segment{
			Begin: c.ValueOrOffset[timeIDs[runStart]],
			End:   c.ValueOrOffset[timeIDs[i-1]],
		})
		runStart = i
	}
	return segments
}
