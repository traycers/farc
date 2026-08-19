package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ioengine"
)

func testParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        4096,
		ReadChunkSize:     4096,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

func openStandard(t *testing.T, path string, size int64) ioengine.Backend {
	t.Helper()
	err := CreateSizedFile(path, size, 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestInit_WritesReadableFblock0(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 1 << 20, N: 8, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	err := Init(backend, InitConfig{
		Geometry:    geo,
		Params:      testParams(),
		Now:         1000,
		CatalogPath: filepath.Join(dir, "storage.catalog"),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	buf := make([]byte, geo.FblockSize)
	if _, err := backend.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt fblock0: %v", err)
	}
	h, diag, err := fblock.DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if diag.Status() != fblock.HeaderIntact {
		t.Fatalf("header status = %v, want Intact", diag.Status())
	}
	if h.Prolog.FblockSize != geo.FblockSize || h.Prolog.MaxChannels != geo.MaxChannels {
		t.Fatalf("prolog geometry mismatch: %+v", h.Prolog)
	}
	if h.Prolog.WriteSequence != 1 {
		t.Fatalf("write_sequence = %d, want 1", h.Prolog.WriteSequence)
	}
	if h.Catalog.State(0) != fblock.Uninitialized {
		t.Fatalf("embedded self-state = %v, want Uninitialized (bootstrap write never counts as real content)", h.Catalog.State(0))
	}
	for i := uint32(1); i < geo.N; i++ {
		if h.Catalog.State(i) != fblock.Uninitialized {
			t.Fatalf("fblock %d state = %v, want Uninitialized", i, h.Catalog.State(i))
		}
	}

	epilogBuf := buf[geo.FblockSize-uint64(fblock.EpilogSize):]
	epilog, err := fblock.DecodeEpilog(epilogBuf)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if epilog.TOCSize != 0 {
		t.Fatalf("epilog.TOCSize = %d, want 0", epilog.TOCSize)
	}

	// SSD catalog mirror must match the main disk: fblock 0 stays
	// Uninitialized (the bootstrap write never counts as real content).
	ssdCat, meta, err := LoadSSDCatalog(filepath.Join(dir, "storage.catalog"), geo.MaxChannels, geo.N)
	if err != nil {
		t.Fatalf("LoadSSDCatalog: %v", err)
	}
	if meta.WriteSequence != 1 {
		t.Fatalf("SSD catalog write_sequence = %d, want 1", meta.WriteSequence)
	}
	if meta.Cursor != 0 {
		t.Fatalf("SSD catalog cursor = %d, want 0", meta.Cursor)
	}
	if ssdCat.State(0) != fblock.Uninitialized {
		t.Fatalf("SSD catalog fblock 0 state = %v, want Uninitialized", ssdCat.State(0))
	}
}

func TestInit_RefusesAlreadyInitializedWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 1 << 20, N: 4, MaxChannels: 16}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	cfg := InitConfig{Geometry: geo, Params: testParams(), Now: 1}
	err := Init(backend, cfg)
	if err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := Init(backend, cfg); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Init = %v, want ErrAlreadyInitialized", err)
	}
	cfg.Force = true
	err = Init(backend, cfg)
	if err != nil {
		t.Fatalf("forced re-Init: %v", err)
	}
}

func TestInit_RejectsGeometryBelowMinContainerShare(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	// Tiny fblock, huge channel registry: catalog dominates the block,
	// leaving far less than 70% for a fcontainer.
	geo := Geometry{FblockSize: 4096, N: 4, MaxChannels: 60000}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	err := Init(backend, InitConfig{Geometry: geo, Params: testParams(), Now: 1})
	if err == nil {
		t.Fatal("want an error for a geometry below min_container_share, got nil")
	}
}
