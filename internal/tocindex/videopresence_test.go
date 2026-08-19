package tocindex_test

import (
	"testing"

	"github.com/traycers/farc/internal/tocindex"
)

const second = uint64(1_000_000_000)

// TestVideoPresenceSegments_MergesSmallGapsSplitsLargeGaps is
// .scratch/player-redesign/issues/01-hls-server-timeline-endpoint.md's core
// rule: a <2s gap between consecutive video frames merges into one segment,
// a >=2s gap starts a new one.
func TestVideoPresenceSegments_MergesSmallGapsSplitsLargeGaps(t *testing.T) {
	unit := newTestUnit(t)
	// gap between times[2] (2s) and times[3] (5s) is 3s >= the 2s threshold:
	// two segments, {0,2s} and {5s,6s}.
	times := []uint64{0, second, 2 * second, 5 * second, 6 * second}
	uuid := writeChannelVideo(t, unit, 1, times)
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	segments := tocindex.VideoPresenceSegments(columns, 1)
	want := []tocindex.Segment{
		{Begin: times[0], End: times[2]},
		{Begin: times[3], End: times[4]},
	}
	if len(segments) != len(want) || segments[0] != want[0] || segments[1] != want[1] {
		t.Fatalf("VideoPresenceSegments = %+v, want %+v", segments, want)
	}
}

// TestVideoPresenceSegments_NoVideoForChannel: a channel with only audio (or
// no matching channel at all) has no video-presence segments.
func TestVideoPresenceSegments_NoVideoForChannel(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelAudio(t, unit, 1, []uint64{0, second})
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	segments := tocindex.VideoPresenceSegments(columns, 1)
	if segments != nil {
		t.Fatalf("VideoPresenceSegments = %+v, want nil (channel has audio only)", segments)
	}
}
