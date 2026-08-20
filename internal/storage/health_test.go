package storage

import "testing"

func TestHealthMonitor_RecordBytesWritten_AccumulatesInStats(t *testing.T) {
	h := NewHealthMonitor(nil, 0)

	h.RecordBytesWritten(100)
	h.RecordBytesWritten(50)

	_, _, _, bytesWritten := h.Stats()
	if bytesWritten != 150 {
		t.Fatalf("Stats() bytesWritten = %d, want 150", bytesWritten)
	}
}

func TestHealthMonitor_RecordFblockCompleted_Increments(t *testing.T) {
	h := NewHealthMonitor(nil, 0)

	h.RecordFblockCompleted()
	h.RecordFblockCompleted()

	if got := h.FblocksCompleted(); got != 2 {
		t.Fatalf("FblocksCompleted() = %d, want 2", got)
	}
}

func TestHealthMonitor_RecordFblockSizes_ReturnsRecordsSortedByIndex(t *testing.T) {
	h := NewHealthMonitor(nil, 0)

	// Recorded out of index order -- FblockSizes() must sort, not just echo
	// recording order, so a Prometheus scrape gets a stable label set.
	h.RecordFblockSizes(7, 100, 20, 800)
	h.RecordFblockSizes(3, 100, 10, 900)

	got := h.FblockSizes()
	want := []FblockSizeRecord{
		{Index: 3, CatalogSize: 100, TocSize: 10, ContentSize: 900},
		{Index: 7, CatalogSize: 100, TocSize: 20, ContentSize: 800},
	}
	if len(got) != len(want) {
		t.Fatalf("FblockSizes() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FblockSizes()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestHealthMonitor_RecordFblockSizes_ReusedIndexOverwritesNotAppends covers
// what an append-only []FblockSizeRecord would get wrong: a cyclic storage
// completes the same physical index more than once over its lifetime (that
// is the normal steady state, not an edge case), and a Prometheus gather
// rejects two metrics with identical labels ({storage,fblock}) in one
// scrape -- which would fail the whole /metrics endpoint, not just this
// panel's series. Recording the same index twice must yield exactly one
// record, with the latest sizes.
func TestHealthMonitor_RecordFblockSizes_ReusedIndexOverwritesNotAppends(t *testing.T) {
	h := NewHealthMonitor(nil, 0)

	h.RecordFblockSizes(3, 100, 10, 900)
	h.RecordFblockSizes(3, 100, 15, 700) // same index, fblock 3 completed again

	got := h.FblockSizes()
	want := []FblockSizeRecord{
		{Index: 3, CatalogSize: 100, TocSize: 15, ContentSize: 700},
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("FblockSizes() = %+v, want %+v (exactly one record, latest sizes)", got, want)
	}
}
