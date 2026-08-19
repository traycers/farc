package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// TestConsistencyCheck_RecoversPartialInProgress simulates a crash mid-fill:
// a real Segment (via *Unit's actual write path) gets far enough for at
// least one periodic-flush trigger to land durably (ADR-017's magic-trailer
// combined write), but Close is never called -- exactly like a process
// dying before the fcontainer finished. Reopening the Storage must recover
// the fblock all the way to Ready, with a real, readable TOC and an End
// truncated to whatever was actually confirmed on disk, not just marked
// Bad.
func TestConsistencyCheck_RecoversPartialInProgress(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	imgPath := filepath.Join(dir, "storage.img")

	u := initAndOpen(t, dir, geo, "")

	seg, _, _, err := u.BeginSegment([]uint16{1}, 1000)
	if err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}
	configID, err := seg.AddStreamParams(1, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       100,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	// smallParams' FchunkSize is 1024 -- one large-ish frame crosses it,
	// triggering a periodic flush well before any Close would happen.
	bigFrame := make([]byte, 2048)
	err = seg.AddFrames(configID, []fcontainer.Frame{{Data: bigFrame, Time: 150, Kind: mediatree.FrameKindI}})
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	impl := seg.(*segmentImpl)
	deadline := time.Now().Add(2 * time.Second)
	for impl.handle.Written() <= impl.headerLen && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if impl.handle.Written() <= impl.headerLen {
		t.Fatal("timed out waiting for a periodic flush before simulating the crash")
	}

	// Simulate a crash: never call seg.Close() -- just tear the Unit down
	// directly, exactly as if the process had died right here, after at
	// least one flush trigger landed durably but before the fcontainer was
	// ever finalized.
	if err := u.Close(); err != nil {
		t.Fatalf("simulated crash teardown: %v", err)
	}

	u2 := openExisting(t, imgPath, "")
	defer u2.Close()

	snap := u2.Index().Snapshot()
	if snap.State(0) != fblock.Ready {
		t.Fatalf("state = %v, want Ready (recovered)", snap.State(0))
	}
	if snap.End[0] == 0 || snap.End[0] > 150 {
		t.Fatalf("End[0] = %d, want a recovered value in (0, 150]", snap.End[0])
	}

	uuid := snap.UUID[0]
	columns, err := u2.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	frameDataIDs := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(frameDataIDs) != 1 {
		t.Fatalf("frame_data(video) nodes = %d, want 1", len(frameDataIDs))
	}
	got, err := u2.ReadNodeValue(uuid, columns, frameDataIDs[0])
	if err != nil {
		t.Fatalf("ReadNodeValue: %v", err)
	}
	if len(got) != len(bigFrame) {
		t.Fatalf("frame data len = %d, want %d", len(got), len(bigFrame))
	}
}

// TestConsistencyCheck_NoTrailerAtAllStillBecomesBad covers the case where
// the header is intact (BeginWrite happened) but not even one flush
// trigger ever landed -- ConsistencyCheck's original, pre-recovery fallback
// still applies.
func TestConsistencyCheck_NoTrailerAtAllStillBecomesBad(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	imgPath := filepath.Join(dir, "storage.img")

	u := initAndOpen(t, dir, geo, "")
	seg, _, _, err := u.BeginSegment([]uint16{1}, 1000)
	if err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}
	// No AddStreamParams/AddFrames at all -- only the header itself is
	// physically written (promoteLocked's EnqueueOpenWrite), no trailer
	// ever gets appended. Wait for the header write itself to land and be
	// confirmed before tearing down, so this test actually exercises
	// "header intact, no trailer" rather than racing a teardown before
	// even the header made it to disk (which would leave fblock 0 looking
	// merely uninitialized, not in_progress, on reopen).
	impl := seg.(*segmentImpl)
	deadline := time.Now().Add(2 * time.Second)
	for impl.handle.Written() < impl.headerLen && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if impl.handle.Written() < impl.headerLen {
		t.Fatal("timed out waiting for the header write to be confirmed")
	}

	if err := u.Close(); err != nil {
		t.Fatalf("simulated crash teardown: %v", err)
	}

	u2 := openExisting(t, imgPath, "")
	defer u2.Close()

	if got := u2.Index().Snapshot().State(0); got != fblock.Bad {
		t.Fatalf("state = %v, want Bad (no trailer ever landed, nothing to recover)", got)
	}
}
