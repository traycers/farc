package index

import (
	"errors"
	"testing"

	"traycers/farc/fblock"
)

func TestRegisterChannelAllocatesLowestFree(t *testing.T) {
	cat := fblock.NewCatalog(4, 1)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	// Both channels are new and belong to the same buffer/write, so they
	// must be resolved together (RegisterChannels) — see the batch
	// protection documented on RegisterChannels: resolving them via two
	// separate RegisterChannel calls with no committed bit in between
	// would let the second call immediately reuse the first's
	// not-yet-referenced position.
	positions, err := m.RegisterChannels([]uint16{100, 200})
	if err != nil {
		t.Fatalf("RegisterChannels: %v", err)
	}
	if positions[0] != 0 || positions[1] != 1 {
		t.Fatalf("RegisterChannels(100,200) = %v, want [0 1]", positions)
	}
	// Registering the same channel again returns the same position.
	if p1again, err := m.RegisterChannel(100); err != nil || p1again != positions[0] {
		t.Fatalf("RegisterChannel(100) again = %d,%v, want %d,nil", p1again, err, positions[0])
	}
}

func TestRegisterChannelsAcrossSeparateWritesReusesCommittedBit(t *testing.T) {
	// Models the realistic cross-write timing: by the time a second write
	// needs a new channel, the first write's channel_bitmap bit has
	// already been committed (Recorder is single-writer, serialized), so
	// its position is no longer RefCount==0 and is not stolen.
	cat := fblock.NewCatalog(2, 4)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	posA, err := m.RegisterChannel(1)
	if err != nil {
		t.Fatalf("RegisterChannel(1): %v", err)
	}
	cat.SetChannelBit(0, posA, true) // commit, as Recorder would for fblock 0's write

	posB, err := m.RegisterChannel(2)
	if err != nil {
		t.Fatalf("RegisterChannel(2): %v", err)
	}
	if posB == posA {
		t.Fatalf("RegisterChannel(2) reused channel 1's already-committed position %d", posA)
	}
}

func TestResolveChannel(t *testing.T) {
	cat := fblock.NewCatalog(4, 1)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)
	if _, ok := m.ResolveChannel(42); ok {
		t.Fatal("unregistered channel should not resolve")
	}
	pos, _ := m.RegisterChannel(42)
	got, ok := m.ResolveChannel(42)
	if !ok || got != pos {
		t.Fatalf("ResolveChannel(42) = %d,%v, want %d,true", got, ok, pos)
	}
}

func TestRegisterChannelReusesZeroRefCountPosition(t *testing.T) {
	const c, n = 2, 3
	cat := fblock.NewCatalog(c, n)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	posA, _ := m.RegisterChannel(1)  // position 0
	cat.SetChannelBit(0, posA, true) // commit channel 1's bit before the next write

	posB, _ := m.RegisterChannel(2) // position 1, registry now full (C=2)
	// Channel 1 (position posA) is referenced by fblock 0; channel 2
	// (position posB) is registered but never referenced by any fblock.

	// A brand new channel number can't get a fresh position (registry
	// full), but posB has RefCount 0, so it should be reused for it —
	// this also implicitly "unregisters" channel 2, matching the doc's
	// reuse rule (a position's old channel number is simply overwritten).
	posC, err := m.RegisterChannel(3)
	if err != nil {
		t.Fatalf("RegisterChannel(3): %v", err)
	}
	if posC != posB {
		t.Fatalf("RegisterChannel(3) = %d, want reused position %d", posC, posB)
	}
	if _, ok := m.ResolveChannel(2); ok {
		t.Error("channel 2's old registration should no longer resolve after its position was reused")
	}
	if got, _ := m.ResolveChannel(3); got != posB {
		t.Errorf("ResolveChannel(3) = %d, want %d", got, posB)
	}
}

func TestRegisterChannelExhaustion(t *testing.T) {
	const c, n = 2, 2
	cat := fblock.NewCatalog(c, n)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	if _, err := m.RegisterChannel(1); err != nil {
		t.Fatalf("RegisterChannel(1): %v", err)
	}
	cat.SetChannelBit(0, 0, true) // commit before registering the next channel

	if _, err := m.RegisterChannel(2); err != nil {
		t.Fatalf("RegisterChannel(2): %v", err)
	}
	cat.SetChannelBit(1, 1, true)
	// Both positions are now allocated and both referenced by at least one
	// fblock, so there is no RefCount==0 position to reuse either.

	if _, err := m.RegisterChannel(3); !errors.Is(err, ErrChannelRegistryFull) {
		t.Fatalf("RegisterChannel(3) = %v, want ErrChannelRegistryFull", err)
	}
}

func TestRegisterChannelsBatchProtectsFreshAllocationsWithinOneCall(t *testing.T) {
	// The actual bug this package's RegisterChannels design fixes: three
	// brand-new channels resolved together for one buffer must not steal
	// each other's freshly (this-call) allocated positions, even though
	// none of their bits are committed anywhere yet.
	const c, n = 3, 2
	cat := fblock.NewCatalog(c, n)
	m := New(cat, 0, fblock.WriteModeCyclic, 30)

	positions, err := m.RegisterChannels([]uint16{10, 20, 30})
	if err != nil {
		t.Fatalf("RegisterChannels: %v", err)
	}
	seen := map[uint16]bool{}
	for _, p := range positions {
		if seen[p] {
			t.Fatalf("positions %v contain a duplicate — a fresh allocation was stolen within the same batch", positions)
		}
		seen[p] = true
	}
	if len(positions) != 3 {
		t.Fatalf("got %d positions, want 3", len(positions))
	}
}
