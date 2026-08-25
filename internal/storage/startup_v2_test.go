package storage

import (
	"path/filepath"
	"testing"

	"github.com/traycers/farc/fblock"
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/ioengine"
)

func TestProbeGeometryV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 4, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{
		FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N,
	})
	if _, err := backend.WriteAt(prologBuf, 0); err != nil {
		t.Fatalf("write prolog: %v", err)
	}

	got, err := probeGeometryV2(backend)
	if err != nil {
		t.Fatalf("probeGeometryV2: %v", err)
	}
	if got != geo {
		t.Fatalf("got %+v, want %+v", got, geo)
	}
}

func TestProbeGeometryV2_UninitializedFblock0(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	backend := openStandard(t, path, 4096)

	if _, err := probeGeometryV2(backend); err == nil {
		t.Fatalf("expected error for uninitialized fblock 0, got nil")
	}
}

// writeCandidateV2 writes a v2.0 fblock with the given write_sequence and,
// if catalogBuf is non-nil, a valid root/params/catalog trio (epilog
// Count==3) — a plausible startup candidate whose catalog is (or isn't,
// if catalogBuf is nil/corrupt) trustworthy.
func writeCandidateV2(t *testing.T, backend ioengine.Backend, geo Geometry, idx uint32, writeSeq uint64, catalogBuf []byte, alignment int) {
	t.Helper()
	base := int64(fblockOffset(geo, idx))
	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{
		FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N, WriteSequence: writeSeq,
	})
	if _, err := backend.WriteAt(prologBuf, base); err != nil {
		t.Fatalf("write prolog(%d): %v", idx, err)
	}
	if catalogBuf == nil {
		return
	}

	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	var epilog fblockv2.Epilog
	offset := uint64(len(prologBuf))
	for i, n := range []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Value: []byte("params")},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Value: catalogBuf},
	} {
		encoded, err := fblockv2.EncodeNode(n, alignment)
		if err != nil {
			t.Fatalf("EncodeNode: %v", err)
		}
		if _, err := backend.WriteAt(encoded, base+int64(offset)); err != nil {
			t.Fatalf("write node %d(%d): %v", i, idx, err)
		}
		epilog.Rows[i] = fblockv2.EpilogRow{Type: n.Type, ID: n.ID, Offset: offset, Size: uint64(len(encoded))}
		epilog.Count++
		if _, err := backend.WriteAt(fblockv2.EncodeEpilog(epilog), epilogOffset); err != nil {
			t.Fatalf("write epilog(%d): %v", idx, err)
		}
		offset += uint64(len(encoded))
	}
}

func TestScanForFreshestCatalogV2_PicksHighestWriteSequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 3, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	alignment := backend.Alignment()

	catA := fblock.NewCatalog(geo.MaxChannels, geo.N)
	catA.UUID[0] = [16]byte{0xAA}
	catABuf, err := fblock.EncodeCatalog(catA)
	if err != nil {
		t.Fatalf("EncodeCatalog A: %v", err)
	}
	catB := fblock.NewCatalog(geo.MaxChannels, geo.N)
	catB.UUID[0] = [16]byte{0xBB}
	catBBuf, err := fblock.EncodeCatalog(catB)
	if err != nil {
		t.Fatalf("EncodeCatalog B: %v", err)
	}

	writeCandidateV2(t, backend, geo, 0, 5, catABuf, alignment)
	writeCandidateV2(t, backend, geo, 1, 10, catBBuf, alignment)

	got, idx, err := scanForFreshestCatalogV2(backend, geo)
	if err != nil {
		t.Fatalf("scanForFreshestCatalogV2: %v", err)
	}
	if idx != 1 {
		t.Fatalf("idx = %d, want 1 (higher write_sequence)", idx)
	}
	if got.UUID[0] != catB.UUID[0] {
		t.Fatalf("catalog UUID = %x, want %x", got.UUID[0], catB.UUID[0])
	}
}

func TestScanForFreshestCatalogV2_FallsBackWhenFreshestCatalogMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 3, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	alignment := backend.Alignment()

	catA := fblock.NewCatalog(geo.MaxChannels, geo.N)
	catA.UUID[0] = [16]byte{0xAA}
	catABuf, err := fblock.EncodeCatalog(catA)
	if err != nil {
		t.Fatalf("EncodeCatalog A: %v", err)
	}

	// idx1 has the highest write_sequence but never got past its fixed
	// prolog (crash before even root was written) -- no catalog to trust.
	writeCandidateV2(t, backend, geo, 0, 5, catABuf, alignment)
	writeCandidateV2(t, backend, geo, 1, 10, nil, alignment)

	got, idx, err := scanForFreshestCatalogV2(backend, geo)
	if err != nil {
		t.Fatalf("scanForFreshestCatalogV2: %v", err)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0 (fallback candidate)", idx)
	}
	if got.UUID[0] != catA.UUID[0] {
		t.Fatalf("catalog UUID = %x, want %x", got.UUID[0], catA.UUID[0])
	}
}

func TestReadParamsAndPrologV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	alignment := backend.Alignment()

	wantParams := fblock.Params{FchunkSize: 4096, ReadChunkSize: 4096, FlushTimeoutNS: fblock.DefaultFlushTimeoutNS, WriteMode: fblock.WriteModeCyclic, Retention: fblock.Retention{Days: 30}, MinContainerShare: fblock.DefaultMinContainerShare}
	paramsBuf, err := fblock.EncodeParams(wantParams)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	cat := fblock.NewCatalog(geo.MaxChannels, geo.N)
	catalogBuf, err := fblock.EncodeCatalog(cat)
	if err != nil {
		t.Fatalf("EncodeCatalog: %v", err)
	}
	writeCandidateV2(t, backend, geo, 0, 7, catalogBuf, alignment)
	// writeCandidateV2 wrote a placeholder params node ("params") -- overwrite
	// fblock 0 fully with the real params/catalog this test cares about
	// instead of duplicating its node-by-node setup.
	buf, err := fblockv2.AssembleFblock(fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N, WriteSequence: 7}, paramsBuf, catalogBuf, nil, nil, alignment, geo.FblockSize)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}
	if _, err := backend.WriteAt(buf, int64(fblockOffset(geo, 0))); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	prolog, gotParams, err := readParamsAndPrologV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("readParamsAndPrologV2: %v", err)
	}
	if prolog.WriteSequence != 7 {
		t.Fatalf("prolog.WriteSequence = %d, want 7", prolog.WriteSequence)
	}
	if gotParams != wantParams {
		t.Fatalf("params = %+v, want %+v", gotParams, wantParams)
	}
}

func TestScanForFreshestCatalogV2_NoCandidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	if _, _, err := scanForFreshestCatalogV2(backend, geo); err == nil {
		t.Fatalf("expected error when no fblock has a valid prolog, got nil")
	}
}
