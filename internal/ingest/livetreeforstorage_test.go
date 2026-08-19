package ingest

import (
	"testing"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// TestIngestManager_LiveTreeForStorage_ReturnsSharedSegmentElements exercises
// the fblock-tree page's live-data source: since every channel of a Storage
// already shares one StorageSegment (storagesegment.go), LiveTreeForStorage
// needs no per-channel loop/merge -- just that shared segment's own
// Elements()/Generation().
func TestIngestManager_LiveTreeForStorage_ReturnsSharedSegmentElements(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	if err := m.AddChannel(testChannelConfig(1, "disk0")); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	policyOf(t, m, 1).SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := m.StartRecording(1, 100, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := policyOf(t, m, 1).HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}

	elems, gen, ok := m.LiveTreeForStorage("disk0")
	if !ok || len(elems) == 0 || gen == 0 {
		t.Fatalf("LiveTreeForStorage(disk0) = elems(%d), gen=%d, ok=%v, want non-empty elems, gen>0, ok=true", len(elems), gen, ok)
	}
}

func TestIngestManager_LiveTreeForStorage_UnknownStorageNotOK(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	_, _, ok := m.LiveTreeForStorage("no-such-storage")
	if ok {
		t.Fatalf("LiveTreeForStorage(unknown) ok = true, want false")
	}
}
