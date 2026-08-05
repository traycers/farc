package hlsapi

import (
	"sync"
	"testing"
)

func TestChannelSet_AddHasRemove(t *testing.T) {
	s := newChannelSet(map[uint16]bool{1: true})
	if !s.Has(1) {
		t.Fatalf("Has(1) = false, want true (from initial map)")
	}
	if s.Has(2) {
		t.Fatalf("Has(2) = true, want false")
	}

	s.Add(2)
	if !s.Has(2) {
		t.Fatalf("Has(2) after Add = false, want true")
	}

	s.Remove(1)
	if s.Has(1) {
		t.Fatalf("Has(1) after Remove = true, want false")
	}

	// Remove of an absent id is a no-op, not an error.
	s.Remove(99)
	if s.Has(99) {
		t.Fatalf("Has(99) = true after removing a never-added id")
	}
}

// TestChannelSet_ConcurrentAddRemoveHas drives concurrent Add/Remove/Has
// against one channelSet -- meant to be run with -race.
func TestChannelSet_ConcurrentAddRemoveHas(t *testing.T) {
	s := newChannelSet(nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id uint16) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.Add(id)
				s.Has(id)
				s.Remove(id)
			}
		}(uint16(i))
	}
	wg.Wait()
}
