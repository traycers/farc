package tocindex_test

import (
	"testing"

	"github.com/traycers/farc/internal/tocindex"
)

func TestChannelIndex_InsertRemoveRecords(t *testing.T) {
	idx := tocindex.NewIndex()
	ci := idx.Channel(9)

	uuidA := [16]byte{1}
	uuidB := [16]byte{2}
	uuidC := [16]byte{3}
	ci.Insert(tocindex.Record{UUID: uuidB, StorageID: "s1", Begin: 200, End: 300})
	ci.Insert(tocindex.Record{UUID: uuidA, StorageID: "s1", Begin: 100, End: 200})
	ci.Insert(tocindex.Record{UUID: uuidC, StorageID: "s1", Begin: 400, End: 500})

	all := ci.All()
	if len(all) != 3 {
		t.Fatalf("All() = %+v, want 3 records", all)
	}
	if all[0].UUID != uuidA || all[1].UUID != uuidB || all[2].UUID != uuidC {
		t.Fatalf("All() not sorted ascending by Begin: %+v", all)
	}

	// [t1,t2] = [150,250] overlaps A ([100,200]) and B ([200,300]) but not C.
	overlap := ci.Records(150, 250)
	if len(overlap) != 2 || overlap[0].UUID != uuidA || overlap[1].UUID != uuidB {
		t.Fatalf("Records(150,250) = %+v, want [A,B]", overlap)
	}

	if _, ok := ci.Lookup(uuidA); !ok {
		t.Fatalf("Lookup(A) = not found")
	}
	ci.Remove(uuidA)
	if _, ok := ci.Lookup(uuidA); ok {
		t.Fatalf("Lookup(A) after Remove = found, want not found")
	}
	if len(ci.All()) != 2 {
		t.Fatalf("All() after Remove = %+v, want 2 records", ci.All())
	}

	// Remove of an unknown uuid is a no-op, not an error.
	ci.Remove([16]byte{9, 9, 9})
	if len(ci.All()) != 2 {
		t.Fatalf("All() after removing unknown uuid = %+v, want unchanged", ci.All())
	}
}

// TestIndex_Remove_ClearsChannel is ADR-021's direct regression test:
// without Remove, records from a channel's old storage would linger
// forever after a live reassignment (internal/hlsd's reconciliation), and a
// later Insert under the new storage could be shadowed by stale data with
// the same UUID.
func TestIndex_Remove_ClearsChannel(t *testing.T) {
	idx := tocindex.NewIndex()
	uuid := [16]byte{1}
	idx.Channel(1).Insert(tocindex.Record{UUID: uuid, StorageID: "disk0", Begin: 100, End: 200})

	idx.Remove(1)

	if _, ok := idx.Channel(1).Lookup(uuid); ok {
		t.Fatalf("Lookup(uuid) after Remove(1) = found, want not found")
	}
	if len(idx.Channel(1).All()) != 0 {
		t.Fatalf("Channel(1).All() after Remove(1) = %+v, want empty", idx.Channel(1).All())
	}

	// A later Insert under a different storage must not be shadowed by
	// anything left over from before Remove.
	idx.Channel(1).Insert(tocindex.Record{UUID: uuid, StorageID: "disk1", Begin: 300, End: 400})
	rec, ok := idx.Channel(1).Lookup(uuid)
	if !ok {
		t.Fatalf("Lookup(uuid) after re-Insert = not found")
	}
	if rec.StorageID != "disk1" || rec.Begin != 300 {
		t.Fatalf("rec = %+v, want the new disk1 record, not stale disk0 data", rec)
	}
}

// TestChannelIndex_Timeline_ConcatenatesAndClips is .scratch/player-redesign/
// issues/01-hls-server-timeline-endpoint.md's aggregation seam: Timeline
// gathers VideoSegments from every record overlapping [t1,t2] (in time
// order, since Records() already sorts by Begin), clipping segments that
// only partially overlap the requested range.
func TestChannelIndex_Timeline_ConcatenatesAndClips(t *testing.T) {
	idx := tocindex.NewIndex()
	ci := idx.Channel(1)

	uuidA := [16]byte{1}
	uuidB := [16]byte{2}
	ci.Insert(tocindex.Record{
		UUID: uuidA, StorageID: "s1", Begin: 100, End: 200,
		VideoSegments: []tocindex.Segment{{Begin: 100, End: 200}},
	})
	ci.Insert(tocindex.Record{
		UUID: uuidB, StorageID: "s1", Begin: 400, End: 500,
		VideoSegments: []tocindex.Segment{{Begin: 400, End: 450}, {Begin: 480, End: 500}},
	})

	// [150,490] clips A's segment to [150,200] and B's first segment to
	// [480? no -- 480 is within range] -- concretely: A -> {150,200}, B's
	// first segment {400,450} falls entirely before t1=150? no, wait: t1=150
	// only clips the start, A begins at 100 < 150 so its segment is clipped
	// to {150,200}; B's segments both start >=400 > 150 so only the t2=490
	// bound matters, clipping B's second segment {480,500} to {480,490}.
	got := ci.Timeline(150, 490)
	want := []tocindex.Segment{
		{Begin: 150, End: 200},
		{Begin: 400, End: 450},
		{Begin: 480, End: 490},
	}
	if len(got) != len(want) {
		t.Fatalf("Timeline(150,490) = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Timeline(150,490)[%d] = %+v, want %+v (full: got=%+v want=%+v)", i, got[i], want[i], got, want)
		}
	}

	ci.Remove(uuidB)
	got = ci.Timeline(0, 1000)
	want = []tocindex.Segment{{Begin: 100, End: 200}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Timeline(0,1000) after Remove(B) = %+v, want %+v", got, want)
	}
}

func TestIndex_ChannelIsPerChannelAndReused(t *testing.T) {
	idx := tocindex.NewIndex()
	a1 := idx.Channel(1)
	a2 := idx.Channel(1)
	b := idx.Channel(2)

	if a1 != a2 {
		t.Fatalf("Channel(1) returned distinct instances across calls")
	}
	if a1 == b {
		t.Fatalf("Channel(1) and Channel(2) returned the same instance")
	}
}
