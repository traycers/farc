package index

import (
	"errors"
	"testing"

	"traycers/farc/fblock"
)

func newTestCatalog(n uint32) *fblock.Catalog {
	return fblock.NewCatalog(16, n)
}

func TestSelectNextIndexPriority1Uninitialized(t *testing.T) {
	cat := newTestCatalog(5)
	// All uninitialized except index 2 which is Ready.
	cat.SetState(2, fblock.Ready)
	m := New(cat, 2, fblock.WriteModeCyclic, 30)

	idx, err := m.SelectNextIndex(0)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if idx != 3 { // first uninitialized after cursor(2)+1
		t.Errorf("SelectNextIndex = %d, want 3", idx)
	}
}

func TestSelectNextIndexWraparound(t *testing.T) {
	cat := newTestCatalog(5)
	for i := uint32(0); i < 5; i++ {
		cat.SetState(i, fblock.Ready)
	}
	cat.SetState(1, fblock.Uninitialized) // the only free one, before the cursor
	m := New(cat, 3, fblock.WriteModeCyclic, 30)

	idx, err := m.SelectNextIndex(0)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if idx != 1 {
		t.Errorf("SelectNextIndex = %d, want 1 (wraparound)", idx)
	}
}

const day = uint64(24 * 60 * 60 * 1_000_000_000)

func TestSelectNextIndexPriority2Cyclic(t *testing.T) {
	cat := newTestCatalog(3)
	for i := uint32(0); i < 3; i++ {
		cat.SetState(i, fblock.Ready)
		cat.End[i] = 0
	}
	m := New(cat, 0, fblock.WriteModeCyclic, 10) // retention 10 days

	now := 20 * day
	idx, err := m.SelectNextIndex(now)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if idx != 1 { // first past cursor(0)+1
		t.Errorf("SelectNextIndex = %d, want 1", idx)
	}
}

func TestSelectNextIndexProtectedSkipped(t *testing.T) {
	cat := newTestCatalog(2)
	cat.SetState(0, fblock.Ready)
	cat.SetState(1, fblock.Ready)
	cat.SetProtected(1, true)
	m := New(cat, 0, fblock.WriteModeCyclic, 1)

	now := 100 * day
	idx, err := m.SelectNextIndex(now)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if idx != 0 {
		t.Errorf("SelectNextIndex = %d, want 0 (1 is protected)", idx)
	}
}

func TestSelectNextIndexRetentionNotExpired(t *testing.T) {
	cat := newTestCatalog(1)
	cat.SetState(0, fblock.Ready)
	cat.End[0] = 100 * day
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	now := 110 * day // only 10 days since end, retention is 30
	_, err := m.SelectNextIndex(now)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("SelectNextIndex = %v, want ErrNoSpace", err)
	}
}

func TestSelectNextIndexFillUntilFullNeverReusesReady(t *testing.T) {
	cat := newTestCatalog(2)
	cat.SetState(0, fblock.Ready)
	cat.SetState(1, fblock.Ready)
	cat.End[0] = 0
	cat.End[1] = 0
	m := New(cat, 0, fblock.WriteModeFillUntilFull, 1)

	now := 100 * day // both long past retention, but fill_until_full must not care
	_, err := m.SelectNextIndex(now)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("SelectNextIndex = %v, want ErrNoSpace under fill_until_full", err)
	}
}

func TestSelectNextIndexAllInProgress(t *testing.T) {
	cat := newTestCatalog(3)
	for i := uint32(0); i < 3; i++ {
		cat.SetState(i, fblock.InProgress)
	}
	m := New(cat, 0, fblock.WriteModeCyclic, 1)
	if _, err := m.SelectNextIndex(0); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("SelectNextIndex = %v, want ErrNoSpace", err)
	}
}

func TestBeginWriteCompleteWriteMarkBad(t *testing.T) {
	cat := newTestCatalog(2)
	m := New(cat, 1, fblock.WriteModeCyclic, 30)

	idx, err := m.SelectNextIndex(0)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if err := m.BeginWrite(idx); err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	if cat.State(idx) != fblock.InProgress {
		t.Fatalf("state after BeginWrite = %v, want InProgress", cat.State(idx))
	}
	if m.Cursor() != idx {
		t.Fatalf("cursor after BeginWrite = %d, want %d", m.Cursor(), idx)
	}

	var uuid [16]byte
	uuid[0] = 7
	if err := m.CompleteWrite(idx, uuid, 100, 200); err != nil {
		t.Fatalf("CompleteWrite: %v", err)
	}
	if got, ok := m.ResolveUUID(uuid); !ok || got != idx {
		t.Fatalf("ResolveUUID after CompleteWrite = %d,%v, want %d,true", got, ok, idx)
	}

	if err := m.MarkBad(idx); err != nil {
		t.Fatalf("MarkBad: %v", err)
	}
	if cat.State(idx) != fblock.Bad {
		t.Fatalf("state after MarkBad = %v, want Bad", cat.State(idx))
	}
	if _, ok := m.ResolveUUID(uuid); ok {
		t.Fatalf("UUID should be dropped from the index after MarkBad")
	}
}

func TestSetProtectedRequiresReady(t *testing.T) {
	cat := newTestCatalog(1)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	if err := m.SetProtected(0, true); !errors.Is(err, ErrProtectedRequiresReady) {
		t.Fatalf("SetProtected on non-ready = %v, want ErrProtectedRequiresReady", err)
	}
	cat.SetState(0, fblock.Ready)
	if err := m.SetProtected(0, true); err != nil {
		t.Fatalf("SetProtected on ready: %v", err)
	}
	if !cat.Protected(0) {
		t.Fatal("expected protected flag set")
	}
}

func TestIndexOutOfRange(t *testing.T) {
	cat := newTestCatalog(1)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	if err := m.BeginWrite(5); !errors.Is(err, ErrIndexOutOfRange) {
		t.Errorf("BeginWrite(5) = %v, want ErrIndexOutOfRange", err)
	}
	if err := m.CompleteWrite(5, [16]byte{}, 0, 0); !errors.Is(err, ErrIndexOutOfRange) {
		t.Errorf("CompleteWrite(5) = %v, want ErrIndexOutOfRange", err)
	}
	if err := m.MarkBad(5); !errors.Is(err, ErrIndexOutOfRange) {
		t.Errorf("MarkBad(5) = %v, want ErrIndexOutOfRange", err)
	}
}

func TestSnapshotIsIndependentDeepCopy(t *testing.T) {
	cat := newTestCatalog(2)
	m := New(cat, 1, fblock.WriteModeCyclic, 30)

	idx, err := m.SelectNextIndex(0)
	if err != nil {
		t.Fatalf("SelectNextIndex: %v", err)
	}
	if err := m.BeginWrite(idx); err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}

	snap := m.Snapshot()
	if snap.State(idx) != fblock.InProgress {
		t.Fatalf("snapshot state = %v, want InProgress", snap.State(idx))
	}

	// Mutating the snapshot must not affect the Manager's own catalog, and
	// vice versa — Recorder patches UUID/Begin/End into its local snapshot
	// before this write is actually committed via CompleteWrite.
	var uuid [16]byte
	uuid[0] = 42
	snap.UUID[idx] = uuid
	snap.Begin[idx] = 111
	snap.SetChannelBit(idx, 0, true)

	if cat.UUID[idx] != ([16]byte{}) {
		t.Fatalf("mutating the snapshot leaked into the live catalog's UUID")
	}
	if cat.ChannelBit(idx, 0) {
		t.Fatalf("mutating the snapshot leaked into the live catalog's channel_bitmap")
	}

	if err := m.CompleteWrite(idx, uuid, 111, 222); err != nil {
		t.Fatalf("CompleteWrite: %v", err)
	}
	snap2 := m.Snapshot()
	if snap2.UUID[idx] != uuid {
		t.Fatalf("post-CompleteWrite snapshot UUID = %x, want %x", snap2.UUID[idx], uuid)
	}
	snap2.UUID[idx] = [16]byte{}
	if got, ok := m.ResolveUUID(uuid); !ok || got != idx {
		t.Fatalf("mutating a later snapshot leaked back into the live catalog's uuidIndex")
	}
}
