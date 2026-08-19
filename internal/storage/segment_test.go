package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

func TestBeginSegment_AssignsIndexImmediatelyWhenPoolEmpty(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	seg, status, maxSize, err := u.BeginSegment([]uint16{1}, 1000)
	if err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}
	if status != PoolNormal {
		t.Fatalf("status = %v, want PoolNormal", status)
	}
	if maxSize <= 0 {
		t.Fatalf("maxSize = %d, want > 0", maxSize)
	}

	// The physical index must already be in_progress -- before a single
	// AddFrames call -- this is the whole point of early index assignment.
	snap := u.Index().Snapshot()
	foundInProgress := false
	for i := uint32(0); i < snap.N; i++ {
		if snap.State(i) == fblock.InProgress {
			foundInProgress = true
		}
	}
	if !foundInProgress {
		t.Fatal("expected some fblock to already be in_progress right after BeginSegment, before any AddFrames")
	}

	if _, err := seg.Close(2000); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSegmentImpl_IndexNonBlockingWhileMuHeld(t *testing.T) {
	// Close holds s.mu for its entire body (including the disk write it
	// waits on) -- index() (poolSlot's Pool.Slots() surface) must not take
	// the same lock, or a live status query would stall for the whole
	// close instead of ever observing SlotClosing.
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	seg, _, _, err := u.BeginSegment([]uint16{1}, 1000)
	if err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}
	impl := seg.(*segmentImpl)

	impl.mu.Lock()
	defer impl.mu.Unlock()

	done := make(chan struct{})
	go func() {
		impl.index()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("index() blocked on s.mu while it was held (simulating Close in flight) -- must be lock-free")
	}
}

func TestUnit_PoolSlots_ReflectsReservedSegmentAndDefaults(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	slots, err := u.PoolSlots()
	if err != nil {
		t.Fatalf("PoolSlots (empty pool): %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("PoolSlots: want at least one row (PoolTuning.Size)")
	}
	for i, s := range slots {
		if s.State != SlotFree {
			t.Fatalf("slot %d state = %v, want SlotFree (nothing reserved yet)", i, s.State)
		}
		if s.PrologSize == 0 || s.CatalogSize == 0 || s.EpilogSize != fblock.EpilogSize {
			t.Fatalf("slot %d defaults = %+v, want non-zero PrologSize/CatalogSize and EpilogSize=%d", i, s, fblock.EpilogSize)
		}
	}

	seg, _, _, err := u.BeginSegment([]uint16{1}, 1000)
	if err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}

	slots, err = u.PoolSlots()
	if err != nil {
		t.Fatalf("PoolSlots (after BeginSegment): %v", err)
	}
	if slots[0].State != SlotActive {
		t.Fatalf("slot 0 state = %v, want SlotActive (pool was empty, promoted synchronously)", slots[0].State)
	}
	if !slots[0].HasIndex {
		t.Fatalf("slot 0 = %+v, want HasIndex=true", slots[0])
	}

	if _, err := seg.Close(2000); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSegment_PeriodicFlushWritesBeforeClose(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

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
	// smallParams' FchunkSize is 1024 -- one large-ish frame should be
	// enough to cross it and trigger a periodic flush well before Close.
	bigFrame := make([]byte, 2048)
	err = seg.AddFrames(configID, []fcontainer.Frame{{Data: bigFrame, Time: 100, Kind: mediatree.FrameKindI}})
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	// Give the background engine (Unit runs storageengine.Engine.Run on its
	// own goroutine) a moment to actually flush some content -- confirming
	// this happens before Close is the whole point of this test.
	impl := seg.(*segmentImpl)
	deadline := time.Now().Add(2 * time.Second)
	for impl.handle.Written() <= impl.headerLen && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if impl.handle.Written() <= impl.headerLen {
		t.Fatal("timed out waiting for a periodic flush to write some content before Close")
	}

	uuid, err := seg.Close(2000)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := u.ResolveUUID(uuid); !ok {
		t.Fatal("ResolveUUID: not found after Close")
	}
	columns, err := u.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	frameDataIDs := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(frameDataIDs) != 1 {
		t.Fatalf("frame_data(video) nodes = %d, want 1", len(frameDataIDs))
	}
	got, err := u.ReadNodeValue(uuid, columns, frameDataIDs[0])
	if err != nil {
		t.Fatalf("ReadNodeValue: %v", err)
	}
	if len(got) != len(bigFrame) {
		t.Fatalf("frame data len = %d, want %d", len(got), len(bigFrame))
	}
}

// TestSegment_WriteFailureMarksFblockBadAndOpensFreshSegment covers
// .scratch/fblocks-ui/issues/08-ingest-stalls-after-rtp-packet-loss.md's
// silent-failure fix: an open segment whose periodic flush corrupts must
// mark its fblock Bad and free its pool slot, instead of the caller
// silently piling frames into a job the engine will never look at again.
func TestSegment_WriteFailureMarksFblockBadAndOpensFreshSegment(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	imgPath := filepath.Join(dir, "storage.img")
	if err := CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644); err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	plain, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	if err := Init(plain, InitConfig{Geometry: geo, Params: smallParams(), Now: 1}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	backend, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// Corrupt the whole content region of fblock 0 (the first real write's
	// target, per today's fblock-0 fix) so its first periodic flush's
	// write-verify fails -- same technique as
	// TestWriteFcontainer_MidFchunkFailureRetriesOnNewIndex.
	paramsBuf, err := fblock.EncodeParams(smallParams())
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	catalogSize := fblock.CatalogSize(geo.MaxChannels, geo.N)
	offs := fblock.ComputeOffsets(uint32(len(paramsBuf)), catalogSize, 1) // ioengine.OpenStandard, Alignment()==1
	corruptStart := int64(fblockOffset(geo, 0)) + int64(offs.ContentOffset)
	corruptEnd := int64(fblockOffset(geo, 0)) + int64(geo.FblockSize)
	cb := &corruptingBackend{Backend: backend, rangeStart: corruptStart, rangeEnd: corruptEnd}

	u, err := Open(OpenConfig{Backend: cb})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer u.Close()

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

	// smallParams' FchunkSize is 1024 -- this frame crosses it, triggering
	// the periodic flush that will hit the corrupted content region. The
	// flush itself runs on the Unit's own background engine goroutine, so
	// the failure only surfaces on a LATER AddFrames call, once that flush
	// has actually run -- poll with small frames until it does.
	bigFrame := make([]byte, 2048)
	err = seg.AddFrames(configID, []fcontainer.Frame{{Data: bigFrame, Time: 100, Kind: mediatree.FrameKindI}})
	if err != nil {
		t.Fatalf("AddFrames (first): %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err = seg.AddFrames(configID, []fcontainer.Frame{{Data: []byte("x"), Time: 101, Kind: mediatree.FrameKindP}})
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the corrupted flush to fail")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !errors.Is(err, ErrSegmentClosed) {
		t.Fatalf("AddFrames after corrupted flush = %v, want ErrSegmentClosed", err)
	}

	if got := u.Index().Snapshot().State(0); got != fblock.Bad {
		t.Fatalf("fblock 0 state = %v, want Bad", got)
	}

	seg2, _, _, err := u.BeginSegment([]uint16{1}, 2000)
	if err != nil {
		t.Fatalf("BeginSegment (retry): %v", err)
	}
	if _, err := seg2.AddStreamParams(1, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       2000,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	}); err != nil {
		t.Fatalf("AddStreamParams on fresh segment: %v", err)
	}
	idx, ok := seg2.(*segmentImpl).index()
	if !ok || idx != 1 {
		t.Fatalf("fresh segment index = %d,%v, want 1,true (not the Bad fblock 0)", idx, ok)
	}
}

// TestSegment_FullnessClosesFblockAsReadyAndOpensFreshSegment covers
// .scratch/multi-channel-fcontainer/issues/03-no-fullness-driven-fblock-rotation.md:
// a continuous segment that's never explicitly closed must still close
// itself once its content reaches the fblock's own capacity, promoting it
// to Ready (not Bad -- reaching capacity is a successful close, unlike
// TestSegment_WriteFailureMarksFblockBadAndOpensFreshSegment's write
// failure), instead of silently writing past the fblock's own bounds.
func TestSegment_FullnessClosesFblockAsReadyAndOpensFreshSegment(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

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

	// smallGeometry's 8192-byte FblockSize has only a few KB of real content
	// capacity once params/catalog/TOC overhead is accounted for -- keep
	// adding 200-byte frames until the segment closes itself, well within a
	// generous iteration bound so a regression (never closing) fails fast
	// instead of hanging.
	frameData := make([]byte, 200)
	var addErr error
	for i := 0; i < 1000; i++ {
		addErr = seg.AddFrames(configID, []fcontainer.Frame{{Data: frameData, Time: uint64(101 + i), Kind: mediatree.FrameKindP}})
		if addErr != nil {
			break
		}
	}
	if !errors.Is(addErr, ErrSegmentClosed) {
		t.Fatalf("AddFrames once full = %v, want ErrSegmentClosed", addErr)
	}

	if got := u.Index().Snapshot().State(0); got != fblock.Ready {
		t.Fatalf("fblock 0 state = %v, want Ready", got)
	}

	seg2, _, _, err := u.BeginSegment([]uint16{1}, 2000)
	if err != nil {
		t.Fatalf("BeginSegment (fresh): %v", err)
	}
	idx, ok := seg2.(*segmentImpl).index()
	if !ok || idx != 1 {
		t.Fatalf("fresh segment index = %d,%v, want 1,true (not the now-Ready fblock 0)", idx, ok)
	}
}

// TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend is a regression
// test for two bugs found while verifying issue 03's e2e test against a
// real O_DIRECT backend, both now fixed:
//
//  1. writeTailLocked positioned its tail write using WriteHandle.Written()
//     (which deliberately excludes headerPadLen -- issue 08), instead of a
//     value that accounts for that padding still being physically on disk.
//     Fixed via WriteHandle.TrailerOffset().
//  2. fblock.ComputeOffsets's ContentOffset didn't account for the header
//     alignment gap at all, so a reader would locate content at the wrong
//     byte offset even once (1)'s write stopped erroring --
//     .scratch/fblocks-ui/issues/10-header-pad-content-offset-mismatch.md.
//     Fixed by making ComputeOffsets/ContentSize/MaxContainerSize
//     alignment-aware, and reserving the same gap uniformly in the
//     one-shot write path (assembleHeaderAndMagic) too.
//
// Every other Close()-exercising test in this package uses
// ioengine.OpenStandard (Alignment()==1, no gap ever reserved), which could
// never expose either bug -- alignmentEnforcingBackend rejects any
// misaligned WriteAt without needing a real O_DIRECT filesystem. A test
// that only checked Close() didn't error would have missed bug (2) — a
// silent offset shift, not an error — so this reads the content back for
// real afterward, exactly like TestSegment_CloseProducesReadableFcontainer.
func TestSegment_CloseWritesAlignedTailOnPaddedHeaderBackend(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	imgPath := filepath.Join(dir, "storage.img")
	if err := CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644); err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	plain, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	if err := Init(plain, InitConfig{Geometry: geo, Params: smallParams(), Now: 1}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	backend, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	aligned := &alignmentEnforcingBackend{Backend: backend, align: 512}

	u, err := Open(OpenConfig{Backend: aligned})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer u.Close()

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
	if err := seg.AddFrames(configID, []fcontainer.Frame{{Data: []byte("hello-frame"), Time: 100, Kind: mediatree.FrameKindI}}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	uuid, err := seg.Close(1000)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	columns, err := u.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	frameDataIDs := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(frameDataIDs) != 1 {
		t.Fatalf("frame_data(video) nodes = %d, want 1", len(frameDataIDs))
	}
	got, err := u.ReadNodeValue(uuid, columns, frameDataIDs[0])
	if err != nil {
		t.Fatalf("ReadNodeValue: %v", err)
	}
	if string(got) != "hello-frame" {
		t.Fatalf("frame data = %q, want %q (content misaligned relative to where ComputeOffsets told the reader to look)", got, "hello-frame")
	}
}

func TestSegment_CloseProducesReadableFcontainer(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

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
	err = seg.AddFrames(configID, []fcontainer.Frame{{Data: []byte("hello-frame"), Time: 100, Kind: mediatree.FrameKindI}})
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	uuid, err := seg.Close(1000)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0 (fblock 0's bootstrap write doesn't count as real content)", idx)
	}

	columns, err := u.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	frameDataIDs := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(frameDataIDs) != 1 {
		t.Fatalf("frame_data(video) nodes = %d, want 1", len(frameDataIDs))
	}
	got, err := u.ReadNodeValue(uuid, columns, frameDataIDs[0])
	if err != nil {
		t.Fatalf("ReadNodeValue: %v", err)
	}
	if string(got) != "hello-frame" {
		t.Fatalf("frame data = %q, want %q", got, "hello-frame")
	}

	cands := u.Candidates(1, 50, 150)
	if len(cands) != 1 || cands[0] != idx {
		t.Fatalf("Candidates(1,50,150) = %v, want [%d]", cands, idx)
	}
}
