package storage

import (
	"errors"
	"testing"
)

type fakePoolSlot struct {
	promoted    bool
	failPromote error
	idx         uint32
	closingFlag bool
	content     int64
	elements    int
}

func (f *fakePoolSlot) promoteLocked(now uint64, occupied int) error {
	if f.failPromote != nil {
		return f.failPromote
	}
	f.promoted = true
	return nil
}

func (f *fakePoolSlot) index() (uint32, bool) {
	return f.idx, f.promoted
}

func (f *fakePoolSlot) closing() bool {
	return f.closingFlag
}

func (f *fakePoolSlot) contentBytes() int64 {
	return f.content
}

func (f *fakePoolSlot) elementCount() int {
	return f.elements
}

func newTestPool(size, warnAt, pressAt int) *Pool {
	return newPool(PoolTuning{Size: size, WarningAt: warnAt, BackpressureAt: pressAt})
}

func TestPool_ReserveExhaustedReturnsError(t *testing.T) {
	p := newTestPool(1, 1, 1)
	seg1 := &fakePoolSlot{}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}
	if !seg1.promoted {
		t.Fatal("seg1 should be promoted immediately (pool was empty)")
	}

	seg2 := &fakePoolSlot{}
	_, err := p.reserve(seg2, 1000)
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("reserve(seg2) on a full pool: err = %v, want ErrPoolExhausted", err)
	}
}

func TestPool_OnlyHeadIsPromoted(t *testing.T) {
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{}
	seg2 := &fakePoolSlot{}

	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}
	if _, err := p.reserve(seg2, 1000); err != nil {
		t.Fatalf("reserve(seg2): %v", err)
	}
	if !seg1.promoted {
		t.Fatal("seg1 (FIFO head) should be promoted")
	}
	if seg2.promoted {
		t.Fatal("seg2 (queued behind seg1, no free physical index) should NOT be promoted yet")
	}
}

func TestPool_ReleasePromotesNextQueued(t *testing.T) {
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{}
	seg2 := &fakePoolSlot{}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}
	if _, err := p.reserve(seg2, 1000); err != nil {
		t.Fatalf("reserve(seg2): %v", err)
	}

	if err := p.release(seg1, 2000); err != nil {
		t.Fatalf("release(seg1): %v", err)
	}
	if !seg2.promoted {
		t.Fatal("seg2 should be promoted once it becomes the new FIFO head")
	}

	// The pool has a free slot again now.
	seg3 := &fakePoolSlot{}
	if _, err := p.reserve(seg3, 2000); err != nil {
		t.Fatalf("reserve(seg3) after release: %v", err)
	}
}

func TestPool_Slots_DefaultsAppliedToEveryRow(t *testing.T) {
	// Prolog/catalog/epilog are Storage-wide constants (same for every
	// fblock), not per-slot state -- the caller (Unit) computes them
	// fresh each call and Pool denormalizes them into every row, free
	// rows included, so a free row renders at the same expected sizes as
	// an occupied one (.scratch/fblocks-ui/issues/04-pool-status-list-plan.md).
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{idx: 7}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}

	defaults := SectionSizes{PrologSize: 100, CatalogSize: 200, EpilogSize: 20}
	slots := p.Slots(defaults)
	for i, s := range slots {
		if s.PrologSize != defaults.PrologSize || s.CatalogSize != defaults.CatalogSize || s.EpilogSize != defaults.EpilogSize {
			t.Fatalf("slot %d sizes = %+v, want defaults %+v applied to every row", i, s, defaults)
		}
	}
}

func TestPool_Slots_EmptyPoolAllFree(t *testing.T) {
	p := newTestPool(3, 10, 10)
	slots := p.Slots(SectionSizes{})
	if len(slots) != 3 {
		t.Fatalf("len(Slots()) = %d, want 3 (always PoolTuning.Size entries)", len(slots))
	}
	for i, s := range slots {
		if s.State != SlotFree {
			t.Fatalf("slot %d state = %v, want SlotFree", i, s.State)
		}
	}
}

func TestPool_Slots_OccupiedHeadIsActiveWithIndex(t *testing.T) {
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{idx: 7}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}

	slots := p.Slots(SectionSizes{})
	if slots[0].State != SlotActive {
		t.Fatalf("slot 0 state = %v, want SlotActive", slots[0].State)
	}
	if !slots[0].HasIndex || slots[0].Index != 7 {
		t.Fatalf("slot 0 = %+v, want HasIndex=true Index=7", slots[0])
	}
	if slots[1].State != SlotFree {
		t.Fatalf("slot 1 state = %v, want SlotFree", slots[1].State)
	}
}

func TestPool_Slots_QueuedNonHeadHasNoIndexYet(t *testing.T) {
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{idx: 7}
	seg2 := &fakePoolSlot{idx: 8}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}
	if _, err := p.reserve(seg2, 1000); err != nil {
		t.Fatalf("reserve(seg2): %v", err)
	}

	slots := p.Slots(SectionSizes{})
	if slots[1].State != SlotQueued {
		t.Fatalf("slot 1 state = %v, want SlotQueued", slots[1].State)
	}
	if slots[1].HasIndex {
		t.Fatalf("slot 1 = %+v, want HasIndex=false (not yet promoted)", slots[1])
	}
}

func TestPool_Slots_ClosingHeadOverridesActive(t *testing.T) {
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{idx: 7}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}

	seg1.closingFlag = true

	slots := p.Slots(SectionSizes{})
	if slots[0].State != SlotClosing {
		t.Fatalf("slot 0 state = %v, want SlotClosing", slots[0].State)
	}
	if !slots[0].HasIndex || slots[0].Index != 7 {
		t.Fatalf("slot 0 = %+v, want HasIndex=true Index=7 (still the fblock being closed)", slots[0])
	}
}

func TestPool_Slots_ContentAndTOCSizesTrackLiveFill(t *testing.T) {
	// 3072 and 128 are toc.EncodedSize(100)/toc.EncodedSize(0), hand-
	// verified independently against the closed-form formula in
	// TestEncodedSizeMatchesFormula (toc/columns_test.go) -- this test
	// checks Pool's wiring (elementCount()/contentBytes() -> the row),
	// not the formula itself.
	p := newTestPool(2, 10, 10)
	seg1 := &fakePoolSlot{idx: 7, content: 4096, elements: 100}
	if _, err := p.reserve(seg1, 1000); err != nil {
		t.Fatalf("reserve(seg1): %v", err)
	}

	slots := p.Slots(SectionSizes{})
	if slots[0].ContentSize != 4096 {
		t.Fatalf("slot 0 ContentSize = %d, want 4096", slots[0].ContentSize)
	}
	if slots[0].TOCSize != 3072 {
		t.Fatalf("slot 0 TOCSize = %d, want 3072 (toc.EncodedSize(100))", slots[0].TOCSize)
	}
	if slots[1].ContentSize != 0 || slots[1].TOCSize != 128 {
		t.Fatalf("slot 1 (free) = %+v, want ContentSize=0 TOCSize=128 (toc.EncodedSize(0), the expected/default empty size)", slots[1])
	}
}

func TestPool_StatusThresholds(t *testing.T) {
	p := newTestPool(4, 2, 3)
	if got := p.Status(); got != PoolNormal {
		t.Fatalf("empty pool: Status() = %v, want PoolNormal", got)
	}
	p.reserve(&fakePoolSlot{}, 1000)
	if got := p.Status(); got != PoolNormal {
		t.Fatalf("1 occupied: Status() = %v, want PoolNormal", got)
	}
	p.reserve(&fakePoolSlot{}, 1000)
	if got := p.Status(); got != PoolWarning {
		t.Fatalf("2 occupied: Status() = %v, want PoolWarning", got)
	}
	p.reserve(&fakePoolSlot{}, 1000)
	if got := p.Status(); got != PoolBackpressure {
		t.Fatalf("3 occupied: Status() = %v, want PoolBackpressure", got)
	}
}
