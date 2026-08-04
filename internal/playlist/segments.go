package playlist

import (
	"sort"

	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// SegRange is one segment's time bounds within a single fcontainer.
type SegRange struct {
	Start uint64
	End   uint64
}

// keyframeTimesInRange returns, ascending, every video keyframe (frame_kind
// == I) timestamp among frame_time nodes in [start,stop) — the set of valid
// segment-boundary snap points (an HLS segment must begin at a keyframe).
// Node-id order equals time order within one subtree (docs/docs/archive/
// 06-toc-format.md §7), so the result is already sorted.
func keyframeTimesInRange(c *toc.Columns, start, stop uint32) []uint64 {
	var out []uint64
	for _, timeID := range toc.InRange(toc.ScanByRole(c, mediatree.RoleFrameTimeVideo), start, stop) {
		frameID := c.Parent[timeID]
		kindID, ok := findChildByRole(c, frameID, mediatree.RoleFrameKind)
		if !ok {
			continue
		}
		v, ok := toc.InlineValue(c, kindID)
		if !ok || len(v) != 1 || v[0] != mediatree.FrameKindI {
			continue
		}
		out = append(out, c.ValueOrOffset[timeID])
	}
	return out
}

// earliestAtOrAfter returns the smallest element of sorted that is >=
// target.
func earliestAtOrAfter(sorted []uint64, target uint64) (uint64, bool) {
	i := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= target })
	if i >= len(sorted) {
		return 0, false
	}
	return sorted[i], true
}

// buildRecordSegments lays out the segment grid for one fcontainer's
// [windowStart, windowEnd) portion of channel (ADR-019: the grid restarts at
// each fcontainer and never crosses its boundary). Each boundary after the
// first snaps forward to the nearest keyframe at or after
// windowStart + n*targetDurNS (PLAN.md's Gap resolutions), clipped to
// windowEnd — producing one deliberately short final segment rather than
// dropping the remainder.
func buildRecordSegments(columns *toc.Columns, channel uint16, windowStart, windowEnd, targetDurNS uint64) []SegRange {
	if windowStart >= windowEnd {
		return nil
	}
	start, stop, ok := channelSubtree(columns, channel)
	if !ok {
		return nil
	}
	keyframes := keyframeTimesInRange(columns, start, stop)

	boundaries := []uint64{windowStart}
	for {
		next := windowStart + uint64(len(boundaries))*targetDurNS
		if next >= windowEnd {
			break
		}
		kf, ok := earliestAtOrAfter(keyframes, next)
		if !ok || kf >= windowEnd || kf <= boundaries[len(boundaries)-1] {
			break
		}
		boundaries = append(boundaries, kf)
	}
	boundaries = append(boundaries, windowEnd)

	segs := make([]SegRange, 0, len(boundaries)-1)
	for i := 0; i < len(boundaries)-1; i++ {
		if boundaries[i] >= boundaries[i+1] {
			continue
		}
		segs = append(segs, SegRange{Start: boundaries[i], End: boundaries[i+1]})
	}
	return segs
}
