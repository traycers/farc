package tocindex_test

import (
	"testing"

	"traycers/farc/internal/tocindex"
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
