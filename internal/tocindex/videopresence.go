package tocindex

import (
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// gapThresholdNS mirrors internal/vaablocks.GapThresholdNS's 2-second rule.
// Duplicated intentionally, not imported: internal/vaablocks exists purely
// for the msm_server integration, and hls_server's video-presence timeline
// (.scratch/player-redesign/) must not depend on it.
const gapThresholdNS = uint64(2_000_000_000)

// Segment is one continuous run of a channel's video frames, split wherever
// the gap to the next frame is >= gapThresholdNS.
type Segment struct {
	Begin uint64
	End   uint64
}

// VideoPresenceSegments returns channel's video-presence segments from c, in
// increasing-time order -- nil if channel isn't present in c or has no video
// frames. Unlike internal/vaablocks.Compute, this only needs frame
// timestamps: it exists purely to answer "did this channel have video at
// time t", not to identify a specific config/stream for msm, so it skips
// vaablocks' byte-offset/config/stream resolution entirely.
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
