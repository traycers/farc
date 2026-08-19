package playlist_test

import (
	"strings"
	"testing"
	"time"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/playlist"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

type frameSpec struct {
	Time uint64
	Kind uint8
	Data string
}

// buildColumns builds a standalone *toc.Columns for one fcontainer, without
// going through storage.Unit at all — the same three calls
// internal/storage/recorder.go's WriteFcontainer itself makes
// (filler.Elements -> mediatree.EncodeContent/DecodeContentWithOffsets ->
// toc.Build), reused here since playlist only ever consumes already-decoded
// Columns (via tocindex.Record) and never touches Storage.
func buildColumns(t *testing.T, channel uint32, sps, pps []byte, frames []frameSpec) *toc.Columns {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frames[0].Time,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   sps,
		ParamPPS:   pps,
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	fcFrames := make([]fcontainer.Frame, len(frames))
	for i, fr := range frames {
		fcFrames[i] = fcontainer.Frame{Data: []byte(fr.Data), Time: fr.Time, Kind: fr.Kind}
	}
	err = f.AddFrames(configID, fcFrames)
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	elems := f.Elements()
	contentBuf := mediatree.EncodeContent(elems)
	_, valueOffsets, err := mediatree.DecodeContentWithOffsets(contentBuf)
	if err != nil {
		t.Fatalf("DecodeContentWithOffsets: %v", err)
	}
	columns, err := toc.Build(elems, valueOffsets)
	if err != nil {
		t.Fatalf("toc.Build: %v", err)
	}
	return columns
}

// tenFrames lays out 10 frames one second apart, keyframes every 2 seconds
// (I,P,I,P,...) starting at startSec.
func tenFrames(startSec int) []frameSpec {
	const second = uint64(1e9)
	out := make([]frameSpec, 10)
	for i := range out {
		kind := mediatree.FrameKindP
		if i%2 == 0 {
			kind = mediatree.FrameKindI
		}
		out[i] = frameSpec{Time: uint64(startSec)*second + uint64(i)*second, Kind: kind, Data: "frame"}
	}
	return out
}

func TestBuild_SingleRecordSplitsAtKeyframes(t *testing.T) {
	const second = uint64(1e9)
	columns := buildColumns(t, 1, []byte{1, 2, 3}, []byte{4, 5}, tenFrames(0))

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: 9 * second, Columns: columns})

	m3u8, err := playlist.Build(idx, 1, 0, 9*second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := "#EXTM3U\n" +
		"#EXT-X-VERSION:7\n" +
		"#EXT-X-TARGETDURATION:4\n" +
		"#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXT-X-PLAYLIST-TYPE:VOD\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXT-X-MAP:URI=\"/segments/1/s1/01000000000000000000000000000000/init.mp4\"\n" +
		"#EXTINF:4.000,\n" +
		"/segments/1/s1/01000000000000000000000000000000/0/seg.m4s\n" +
		"#EXTINF:2.000,\n" +
		"/segments/1/s1/01000000000000000000000000000000/1/seg.m4s\n" +
		"#EXTINF:2.000,\n" +
		"/segments/1/s1/01000000000000000000000000000000/2/seg.m4s\n" +
		"#EXTINF:1.000,\n" +
		"/segments/1/s1/01000000000000000000000000000000/3/seg.m4s\n" +
		"#EXT-X-ENDLIST\n"
	if m3u8 != want {
		t.Fatalf("Build() =\n%s\nwant:\n%s", m3u8, want)
	}
}

func TestBuild_TwoRecordsSameConfig_NoDiscontinuity(t *testing.T) {
	const second = uint64(1e9)
	sps, pps := []byte{1, 2, 3}, []byte{4, 5}
	columns1 := buildColumns(t, 1, sps, pps, tenFrames(0))
	columns2 := buildColumns(t, 1, sps, pps, []frameSpec{
		{Time: 9 * second, Kind: mediatree.FrameKindI, Data: "a"},
		{Time: 18 * second, Kind: mediatree.FrameKindI, Data: "b"},
	})

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: 9 * second, Columns: columns1})
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{2}, StorageID: "s1", Begin: 9 * second, End: 18 * second, Columns: columns2})

	m3u8, err := playlist.Build(idx, 1, 0, 18*second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(m3u8, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("Build() = %s, want no discontinuity for identical config", m3u8)
	}
	if !strings.Contains(m3u8, "/segments/1/s1/02000000000000000000000000000000/init.mp4") {
		t.Fatalf("Build() = %s, want an init segment reference for the second record", m3u8)
	}
}

func TestBuild_TwoRecordsDifferentConfig_Discontinuity(t *testing.T) {
	const second = uint64(1e9)
	columns1 := buildColumns(t, 1, []byte{1, 2, 3}, []byte{4, 5}, tenFrames(0))
	columns2 := buildColumns(t, 1, []byte{9, 9, 9, 9}, []byte{4, 5}, []frameSpec{
		{Time: 9 * second, Kind: mediatree.FrameKindI, Data: "a"},
		{Time: 18 * second, Kind: mediatree.FrameKindI, Data: "b"},
	})

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: 9 * second, Columns: columns1})
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{2}, StorageID: "s1", Begin: 9 * second, End: 18 * second, Columns: columns2})

	m3u8, err := playlist.Build(idx, 1, 0, 18*second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := "#EXT-X-DISCONTINUITY\n#EXT-X-MAP:URI=\"/segments/1/s1/02000000000000000000000000000000/init.mp4\"\n"
	if !strings.Contains(m3u8, want) {
		t.Fatalf("Build() =\n%s\nwant it to contain:\n%s", m3u8, want)
	}
}

func TestBuild_TrailingRecordTooShortForOneSegment(t *testing.T) {
	const second = uint64(1e9)
	columns := buildColumns(t, 1, []byte{1, 2, 3}, []byte{4, 5}, []frameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, Data: "a"},
		{Time: second, Kind: mediatree.FrameKindP, Data: "b"},
	})

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: second, Columns: columns})

	m3u8, err := playlist.Build(idx, 1, 0, second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := strings.Count(m3u8, "#EXTINF"); n != 1 {
		t.Fatalf("Build() = %s, want exactly one (short) segment, got %d", m3u8, n)
	}
	if !strings.Contains(m3u8, "#EXTINF:1.000,\n") {
		t.Fatalf("Build() = %s, want a single 1.000s segment", m3u8)
	}
}

// TestBuild_SegmentBoundsAreQueryIndependent guards the fix this phase made
// to Build: a record's segment grid (and therefore a given segIndex's exact
// [Start,End)) must be the same regardless of which [t1,t2] window a
// request asks for, since internal/hlsapi's segment route only ever
// receives (channel, storage, uuid, segIndex) — it has to recompute the
// same bounds RecordSegments produced when Build advertised that URI.
func TestBuild_SegmentBoundsAreQueryIndependent(t *testing.T) {
	const second = uint64(1e9)
	columns := buildColumns(t, 1, []byte{1, 2, 3}, []byte{4, 5}, tenFrames(0))
	rec := tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: 9 * second, Columns: columns}

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(rec)

	fullWindow, err := playlist.Build(idx, 1, 0, 9*second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build (full window): %v", err)
	}
	// A narrower, clipped query over the same record must not reshape or
	// renumber the segments it happens to overlap.
	clippedWindow, err := playlist.Build(idx, 1, 5*second, 9*second, 2500*time.Millisecond)
	if err != nil {
		t.Fatalf("Build (clipped window): %v", err)
	}
	if !strings.Contains(clippedWindow, "/segments/1/s1/01000000000000000000000000000000/2/seg.m4s") {
		t.Fatalf("clipped Build() = %s, want it to still reference canonical segment index 2 (not renumbered)", clippedWindow)
	}
	if !strings.Contains(fullWindow, "#EXTINF:2.000,\n/segments/1/s1/01000000000000000000000000000000/2/seg.m4s\n") ||
		!strings.Contains(clippedWindow, "#EXTINF:2.000,\n/segments/1/s1/01000000000000000000000000000000/2/seg.m4s\n") {
		t.Fatalf("segment index 2's duration differs between full and clipped windows:\nfull=%s\nclipped=%s", fullWindow, clippedWindow)
	}

	segs := playlist.RecordSegments(rec, 1, 2500*time.Millisecond)
	if len(segs) != 4 || segs[2].Start != 6*second || segs[2].End != 8*second {
		t.Fatalf("RecordSegments = %+v, want canonical segment 2 = [6s,8s)", segs)
	}
}

// TestRecordSegments_FirstBoundarySnapsToNearestKeyframe is the companion
// fix for .scratch/capture-keyframe-start/issues/
// 01-first-frame-not-guaranteed-keyframe.md: a record whose first frame(s)
// aren't keyframes (e.g. pre-fix data, or any other edge case) must still
// produce a first segment starting on a real keyframe, the same way every
// later boundary already does -- not on rec.Begin verbatim.
func TestRecordSegments_FirstBoundarySnapsToNearestKeyframe(t *testing.T) {
	const second = uint64(1e9)
	frames := []frameSpec{
		{Time: 0, Kind: mediatree.FrameKindP, Data: "frame"},
		{Time: 1 * second, Kind: mediatree.FrameKindP, Data: "frame"},
		{Time: 2 * second, Kind: mediatree.FrameKindI, Data: "frame"},
		{Time: 3 * second, Kind: mediatree.FrameKindP, Data: "frame"},
	}
	columns := buildColumns(t, 1, []byte{1, 2, 3}, []byte{4, 5}, frames)
	rec := tocindex.Record{UUID: [16]byte{1}, StorageID: "s1", Begin: 0, End: 4 * second, Columns: columns}

	segs := playlist.RecordSegments(rec, 1, 10*time.Second)
	if len(segs) != 1 || segs[0].Start != 2*second || segs[0].End != 4*second {
		t.Fatalf("RecordSegments = %+v, want a single segment [2s,4s) snapped to the first real keyframe", segs)
	}
}

func TestBuild_NoRecordsIsAnError(t *testing.T) {
	idx := tocindex.NewIndex()
	if _, err := playlist.Build(idx, 1, 0, 1000, time.Second); err == nil {
		t.Fatalf("Build() with no records = nil error, want an error")
	}
}
