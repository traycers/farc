package index

import (
	"testing"

	"traycers/farc/fblock"
)

func TestCandidatesFiltersByChannelTimeAndState(t *testing.T) {
	const c, n = 4, 4
	cat := fblock.NewCatalog(c, n)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	pos, _ := m.RegisterChannel(7)

	// fblock 0: Ready, has channel 7, overlaps [100,200].
	cat.SetState(0, fblock.Ready)
	cat.Begin[0], cat.End[0] = 100, 200
	cat.SetChannelBit(0, pos, true)

	// fblock 1: Ready, has channel 7, but does NOT overlap the query range.
	cat.SetState(1, fblock.Ready)
	cat.Begin[1], cat.End[1] = 1000, 2000
	cat.SetChannelBit(1, pos, true)

	// fblock 2: Ready, overlaps, but does NOT have channel 7.
	cat.SetState(2, fblock.Ready)
	cat.Begin[2], cat.End[2] = 100, 200

	// fblock 3: has channel 7 and overlaps, but is not Ready.
	cat.SetState(3, fblock.InProgress)
	cat.Begin[3], cat.End[3] = 100, 200
	cat.SetChannelBit(3, pos, true)

	got := m.Candidates(7, 150, 160)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("Candidates(7,150,160) = %v, want [0]", got)
	}
}

func TestCandidatesUnknownChannel(t *testing.T) {
	cat := fblock.NewCatalog(4, 4)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	if got := m.Candidates(999, 0, 1); got != nil {
		t.Errorf("Candidates for never-registered channel = %v, want nil", got)
	}
}

func TestNewBuildsUUIDIndexFromExistingCatalog(t *testing.T) {
	cat := fblock.NewCatalog(4, 2)
	cat.SetState(0, fblock.Ready)
	cat.UUID[0] = [16]byte{1, 2, 3}
	cat.SetState(1, fblock.InProgress) // not Ready, must not be indexed

	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	if idx, ok := m.ResolveUUID(cat.UUID[0]); !ok || idx != 0 {
		t.Fatalf("ResolveUUID = %d,%v, want 0,true", idx, ok)
	}
	if _, ok := m.ResolveUUID(cat.UUID[1]); ok {
		t.Fatal("in_progress fblock must not be indexed by UUID")
	}
}
