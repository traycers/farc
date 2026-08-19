package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/index"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storageengine"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

func smallGeometry() Geometry {
	return Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

// initAndOpen creates and initializes a fresh storage image at dir/storage.img
// (with an SSD catalog at dir/storage.catalog unless catalogPath is ""), and
// opens it, returning the live Unit.
func initAndOpen(t *testing.T, dir string, geo Geometry, catalogPath string) *Unit {
	t.Helper()
	imgPath := filepath.Join(dir, "storage.img")
	err := CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = Init(b, InitConfig{Geometry: geo, Params: smallParams(), Now: 1, CatalogPath: catalogPath})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
		t.Fatalf("close after init: %v", err)
	}

	return openExisting(t, imgPath, catalogPath)
}

func openExisting(t *testing.T, imgPath, catalogPath string) *Unit {
	t.Helper()
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	u, err := Open(OpenConfig{Backend: b, CatalogPath: catalogPath})
	if err != nil {
		b.Close()
		t.Fatalf("Open: %v", err)
	}
	return u
}

func videoFiller(t *testing.T, channel uint32, frameData string, frameTime uint64) *fcontainer.Filler {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frameTime,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	if err := f.AddFrames(configID, []fcontainer.Frame{
		{Data: []byte(frameData), Time: frameTime, Kind: mediatree.FrameKindI},
	}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	return f
}

func TestWriteFcontainer_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	f := videoFiller(t, 1, "hello-frame", 100)
	uuid, err := u.WriteFcontainer([]uint16{1}, 100, 100, f, 1000)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
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

func catalogEqual(t *testing.T, a, b *fblock.Catalog) {
	t.Helper()
	if a.N != b.N || a.MaxChannels != b.MaxChannels {
		t.Fatalf("geometry mismatch: N/C %d/%d vs %d/%d", a.N, a.MaxChannels, b.N, b.MaxChannels)
	}
	if !reflect.DeepEqual(a.Flags, b.Flags) {
		t.Fatalf("Flags differ: %v vs %v", a.Flags, b.Flags)
	}
	if !reflect.DeepEqual(a.UUID, b.UUID) {
		t.Fatalf("UUID differ: %v vs %v", a.UUID, b.UUID)
	}
	if !reflect.DeepEqual(a.Begin, b.Begin) {
		t.Fatalf("Begin differ: %v vs %v", a.Begin, b.Begin)
	}
	if !reflect.DeepEqual(a.End, b.End) {
		t.Fatalf("End differ: %v vs %v", a.End, b.End)
	}
	if !reflect.DeepEqual(a.ChannelBitmap, b.ChannelBitmap) {
		t.Fatalf("ChannelBitmap differ: %v vs %v", a.ChannelBitmap, b.ChannelBitmap)
	}
	if !reflect.DeepEqual(a.ChannelRegistry, b.ChannelRegistry) {
		t.Fatalf("ChannelRegistry differ: %v vs %v", a.ChannelRegistry, b.ChannelRegistry)
	}
}

func TestOpen_SSDCatalogPathMatchesFallbackScanPath(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "storage.img")
	catalogPath := filepath.Join(dir, "storage.catalog")
	geo := smallGeometry()

	u := initAndOpen(t, dir, geo, catalogPath)
	// Two overlapping-channel fcontainers, landing on idx 0 and idx 1.
	if _, err := u.WriteFcontainer([]uint16{1, 2}, 100, 200, videoFiller(t, 1, "frame-a", 100), 1000); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if _, err := u.WriteFcontainer([]uint16{2, 3}, 300, 400, videoFiller(t, 2, "frame-b", 300), 2000); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	err := u.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen via path 1 (SSD catalog present and valid).
	u2 := openExisting(t, imgPath, catalogPath)
	cat2 := u2.Index().Snapshot()
	cursor2 := u2.Index().Cursor()
	err = u2.Close()
	if err != nil {
		t.Fatalf("close u2: %v", err)
	}

	err = os.Remove(catalogPath)
	if err != nil {
		t.Fatalf("remove catalog: %v", err)
	}

	// Reopen via path 2 (fallback header scan) — catalogPath is still
	// passed so Open rebuilds it afterward, but LoadSSDCatalog will fail
	// since the file is gone.
	u3 := openExisting(t, imgPath, catalogPath)
	cat3 := u3.Index().Snapshot()
	cursor3 := u3.Index().Cursor()
	defer u3.Close()

	if cursor2 != cursor3 {
		t.Fatalf("cursor mismatch: path1=%d path2=%d", cursor2, cursor3)
	}
	catalogEqual(t, cat2, cat3)

	// §4.3: after a path-2 recovery, the SSD catalog must be rebuilt.
	if _, err := os.Stat(catalogPath); err != nil {
		t.Fatalf("SSD catalog should have been rebuilt after path-2 recovery: %v", err)
	}
}

// writeRawFblockAt writes a fully-assembled fblock for idx directly through
// an Engine (bypassing Recorder/IndexManager entirely), for
// ConsistencyCheck fault-injection tests that need precise control over
// what ends up on disk.
func writeRawFblockAt(t *testing.T, eng *storageengine.Engine, geo Geometry, params fblock.Params, idx uint32, seq uint64, uuid [16]byte, begin, end uint64, content []byte) *fblock.Catalog {
	t.Helper()
	cat := fblock.NewCatalog(geo.MaxChannels, geo.N)
	cat.SetState(idx, fblock.InProgress)
	cat.UUID[idx] = uuid
	cat.Begin[idx] = begin
	cat.End[idx] = end

	h := &fblock.Header{
		Prolog: fblock.FixedProlog{
			FormatVersionMajor: 1,
			MaxChannels:        geo.MaxChannels,
			WriteSequence:      seq,
			FblockSize:         geo.FblockSize,
		},
		Params:  params,
		Catalog: cat,
	}
	buf, err := assembleFblock(h, content, nil, 1) // ioengine.OpenStandard, Alignment()==1
	if err != nil {
		t.Fatalf("assembleFblock: %v", err)
	}
	ticket := eng.EnqueueWrite(int64(fblockOffset(geo, idx)), buf)
	for eng.Step() {
	}
	res, err := ticket.Wait()
	if err != nil || res.Corrupted {
		t.Fatalf("writeRawFblockAt: res=%+v err=%v", res, err)
	}
	return cat
}

// initRawImage creates and initializes a fresh storage image at
// dir/storage.img (no SSD catalog, no Unit) and returns its path.
func initRawImage(t *testing.T, dir string, geo Geometry, params fblock.Params) string {
	t.Helper()
	imgPath := filepath.Join(dir, "storage.img")
	err := CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = Init(b, InitConfig{Geometry: geo, Params: params, Now: 1})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
		t.Fatalf("close after init: %v", err)
	}
	return imgPath
}

func TestConsistencyCheck_ProperlyFinishedInProgressBecomesReady(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	params := smallParams()
	imgPath := initRawImage(t, dir, geo, params)

	backend, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	eng := storageengine.New(backend, engineConfig(params.FchunkSize, params.ReadChunkSize, EngineTuning{}))
	var uuid [16]byte
	uuid[0] = 9
	cat := writeRawFblockAt(t, eng, geo, params, 1, 2, uuid, 100, 200, []byte("payload"))
	err = eng.Close()
	if err != nil {
		t.Fatalf("eng.Close: %v", err)
	}

	// Reload via a fresh backend + a from-scratch IndexManager seeded with
	// this catalog (idx 1 in_progress), and run ConsistencyCheck directly.
	b2, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer b2.Close()
	mgr := index.New(cat, 1, params.WriteMode, params.Retention.Days)
	err = ConsistencyCheck(b2, geo, mgr)
	if err != nil {
		t.Fatalf("ConsistencyCheck: %v", err)
	}
	snap := mgr.Snapshot()
	if snap.State(1) != fblock.Ready {
		t.Fatalf("state = %v, want Ready", snap.State(1))
	}
	if snap.UUID[1] != uuid || snap.Begin[1] != 100 || snap.End[1] != 200 {
		t.Fatalf("metadata not recovered: uuid=%x begin=%d end=%d", snap.UUID[1], snap.Begin[1], snap.End[1])
	}
}

func TestConsistencyCheck_TruncatedEpilogueBecomesBad(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry()
	params := smallParams()
	imgPath := initRawImage(t, dir, geo, params)

	backend, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	eng := storageengine.New(backend, engineConfig(params.FchunkSize, params.ReadChunkSize, EngineTuning{}))
	var uuid [16]byte
	uuid[0] = 7
	cat := writeRawFblockAt(t, eng, geo, params, 1, 2, uuid, 100, 200, []byte("payload"))
	err = eng.Close()
	if err != nil {
		t.Fatalf("eng.Close: %v", err)
	}

	// Simulate a power loss right before the epilogue finished landing:
	// truncate the file a few bytes short of fblock 1's own true end (it's
	// not the last fblock in the file, so truncating relative to the
	// *file's* end would miss it entirely).
	truncateAt := int64(fblockOffset(geo, 1)) + int64(geo.FblockSize) - 5
	err = os.Truncate(imgPath, truncateAt)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	b2, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer b2.Close()
	mgr := index.New(cat, 1, params.WriteMode, params.Retention.Days)
	err = ConsistencyCheck(b2, geo, mgr)
	if err != nil {
		t.Fatalf("ConsistencyCheck: %v", err)
	}
	if got := mgr.Snapshot().State(1); got != fblock.Bad {
		t.Fatalf("state = %v, want Bad", got)
	}
}

// corruptingBackend wraps an ioengine.Backend and flips a byte on read-back
// for the first read whose offset falls in [rangeStart, rangeEnd), exactly
// once — enough to make a single write-verify fchunk fail without also
// breaking the retry's write to a different physical index. A range rather
// than one exact offset, since Segment's write path (segment.go) can split
// one fcontainer's write across more than one physical job (the open
// header/content job, plus a separate tail-write job for whatever wasn't
// periodically flushed) — each restarting its own fchunk boundary count
// from its own job offset, not a single fixed grid from the fblock's start.
type corruptingBackend struct {
	ioengine.Backend
	rangeStart, rangeEnd int64
	fired                bool
}

func (b *corruptingBackend) ReadAt(p []byte, offset int64) (int, error) {
	n, err := b.Backend.ReadAt(p, offset)
	if !b.fired && offset >= b.rangeStart && offset < b.rangeEnd && n > 0 {
		p[0] ^= 0xFF
		b.fired = true
	}
	return n, err
}

// alignmentEnforcingBackend wraps a plain (non-O_DIRECT) backend but
// rejects any WriteAt whose offset or length isn't a multiple of align,
// mirroring internal/ioengine's real DirectBackend.WriteAt closely enough
// to catch alignment bugs in unit tests without needing a real
// O_DIRECT-capable filesystem (every other test in this package uses
// ioengine.OpenStandard, Alignment()==1, which can never exercise this).
// ReadAt is passed through unchanged -- DirectBackend.ReadAt transparently
// handles any offset/length itself (internal/ioengine/direct_linux.go's
// roundRange + scratch buffer), unlike WriteAt, so real callers never need
// aligned reads and this wrapper shouldn't invent a stricter contract than
// the backend it's standing in for.
type alignmentEnforcingBackend struct {
	ioengine.Backend
	align int64
}

func (b *alignmentEnforcingBackend) Alignment() int { return int(b.align) }

func (b *alignmentEnforcingBackend) WriteAt(p []byte, offset int64) (int, error) {
	if offset%b.align != 0 || int64(len(p))%b.align != 0 {
		return 0, ioengine.ErrMisaligned
	}
	return b.Backend.WriteAt(p, offset)
}

func TestWriteFcontainer_MidFchunkFailureRetriesOnNewIndex(t *testing.T) {
	dir := t.TempDir()
	geo := smallGeometry() // FblockSize 8192, FchunkSize 1024 -> 8 chunks/fblock
	imgPath := filepath.Join(dir, "storage.img")
	err := CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	plain, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = Init(plain, InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	err = plain.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	backend, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// The first write after init lands on idx 0 (fblock 0's bootstrap
	// write doesn't count as real content, see Open()'s cursor
	// correction). Corrupt starting exactly at fblock 0's content offset —
	// past every header section Open() itself must read (prolog/params/
	// catalog/checksums), so probing/startup-scan reads are untouched, but
	// the new write's own verify-read of its first content byte fails,
	// forcing a retry onto idx 1.
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

	uuid, err := u.WriteFcontainer([]uint16{1}, 100, 200, videoFiller(t, 1, "retry-frame", 100), 1000)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}

	snap := u.Index().Snapshot()
	if snap.State(0) != fblock.Bad {
		t.Fatalf("idx 0 state = %v, want Bad (the corrupted attempt)", snap.State(0))
	}
	if snap.State(1) != fblock.Ready {
		t.Fatalf("idx 1 state = %v, want Ready (the successful retry)", snap.State(1))
	}
	idx, ok := u.ResolveUUID(uuid)
	if !ok || idx != 1 {
		t.Fatalf("ResolveUUID = %d,%v, want 1,true", idx, ok)
	}
}
