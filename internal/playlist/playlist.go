// Package playlist builds a static VOD HLS playlist (.m3u8) for a
// (channel, t1, t2) playback window, reading only hls_server's local
// tocindex.Index (ADR-018) — no farcd round trip on this hot path. Per
// ADR-019, the segment grid restarts at each fcontainer boundary and never
// crosses it; adjacent fcontainers with a different active codec config get
// a #EXT-X-DISCONTINUITY between them.
package playlist

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"traycers/farc/internal/tocindex"
)

// segment is one playlist entry: a byte-cacheable, independently fetchable
// unit of exactly one fcontainer's data (internal/segmentcache's key shape,
// PLAN.md's package layout).
type segment struct {
	StorageID     string
	UUID          [16]byte
	Index         int // 0-based, within its own fcontainer — canonical, see RecordSegments
	Start, End    uint64
	Discontinuity bool // emit #EXT-X-DISCONTINUITY immediately before this segment
	FirstOfRecord bool // emit #EXT-X-MAP immediately before this segment
}

// RecordSegments returns rec's canonical, full-record segment grid for
// channel: computed over [rec.Begin, rec.End] alone, independent of any
// particular playback query's [t1,t2]. internal/hlsapi's segment route only
// ever receives (channel, storage, uuid, segIndex) — no t1/t2 — so it must
// be able to recompute the exact same [Start,End) bounds for segIndex that
// Build used when it advertised that URI; the grid can only do that if it
// never depends on the requesting query's window, which is exactly what
// this function (and Build, which calls it) guarantees.
func RecordSegments(rec tocindex.Record, channel uint16, targetDur time.Duration) []SegRange {
	return buildRecordSegments(rec.Columns, channel, rec.Begin, rec.End, uint64(targetDur.Nanoseconds()))
}

// Build renders a VOD .m3u8 for channel over [t1, t2] (Unix ns), targeting
// targetDur per segment. A record's segment grid is always computed over
// its own full [Begin,End] range (RecordSegments) — [t1,t2] only selects
// which of those canonical segments overlap the requested window, it never
// reshapes segment boundaries or renumbers indices (see RecordSegments'
// doc). Segment/init URIs match internal/hlsapi's routes (PLAN.md phase 6):
// "/segments/{channel}/{storage}/{uuid}/init.mp4" and
// "/segments/{channel}/{storage}/{uuid}/{n}/seg.m4s".
func Build(index *tocindex.Index, channel uint16, t1, t2 uint64, targetDur time.Duration) (string, error) {
	records := index.Channel(channel).Records(t1, t2)
	if len(records) == 0 {
		return "", fmt.Errorf("playlist: no fcontainers for channel %d in [%d,%d]", channel, t1, t2)
	}

	var segments []segment
	var prev *tocindex.Record
	for _, rec := range records {
		canonical := RecordSegments(rec, channel, targetDur)

		discontinuityPending := prev != nil && configChanged(prev.Columns, rec.Columns, channel)
		first := true
		for i, s := range canonical {
			if s.End <= t1 || s.Start >= t2 {
				continue
			}
			segments = append(segments, segment{
				StorageID:     rec.StorageID,
				UUID:          rec.UUID,
				Index:         i,
				Start:         s.Start,
				End:           s.End,
				Discontinuity: discontinuityPending && first,
				FirstOfRecord: first,
			})
			first = false
		}
		prev = &rec
	}
	if len(segments) == 0 {
		return "", fmt.Errorf("playlist: no segments produced for channel %d in [%d,%d]", channel, t1, t2)
	}

	return render(channel, segments), nil
}

func render(channel uint16, segments []segment) string {
	var maxDurNS uint64
	for _, s := range segments {
		if d := s.End - s.Start; d > maxDurNS {
			maxDurNS = d
		}
	}
	targetDurSeconds := int(math.Ceil(float64(maxDurNS) / 1e9))
	if targetDurSeconds < 1 {
		targetDurSeconds = 1
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", targetDurSeconds)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	for _, s := range segments {
		if s.Discontinuity {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if s.FirstOfRecord {
			fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"/segments/%d/%s/%s/init.mp4\"\n", channel, s.StorageID, hex.EncodeToString(s.UUID[:]))
		}
		durSeconds := float64(s.End-s.Start) / 1e9
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", durSeconds)
		fmt.Fprintf(&b, "/segments/%d/%s/%s/%d/seg.m4s\n", channel, s.StorageID, hex.EncodeToString(s.UUID[:]), s.Index)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}
