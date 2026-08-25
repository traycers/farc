package storage

import (
	"bytes"
	"path/filepath"
	"testing"

	fblockv2 "github.com/traycers/farc/fblock/v2"
)

// TestWriteReadFblockV2RoundTrip proves the v2.0 codec (fblock/v2) round
// trips through a real ioengine.Backend at the geometry-computed offset —
// additive alongside the v1.0 path (headerio.go); nothing in
// internal/storage's real read/write path calls this yet.
func TestWriteReadFblockV2RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 4, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	prolog := fblockv2.FixedProlog{
		FormatVersionMajor: 2,
		FormatVersionMinor: 0,
		MaxChannels:        geo.MaxChannels,
		WriteSequence:      1,
		FblockSize:         geo.FblockSize,
		CatalogEntryCount:  geo.N,
	}
	params := []byte(`{"fchunk_size":4096}`)
	catalog := []byte("catalog-bytes")
	content := []byte("content-bytes-opaque-fcontainer")
	toc := []byte("toc-bytes")

	if err := writeFblockV2(backend, geo, 2, prolog, params, catalog, content, toc); err != nil {
		t.Fatalf("writeFblockV2: %v", err)
	}

	tree, err := readFblockV2(backend, geo, 2)
	if err != nil {
		t.Fatalf("readFblockV2: %v", err)
	}
	if tree.Prolog != prolog {
		t.Fatalf("prolog mismatch: got %+v, want %+v", tree.Prolog, prolog)
	}
	if !bytes.Equal(tree.Params.Value, params) {
		t.Errorf("params value = %q, want %q", tree.Params.Value, params)
	}
	if !bytes.Equal(tree.Catalog.Value, catalog) {
		t.Errorf("catalog value = %q, want %q", tree.Catalog.Value, catalog)
	}
	if !bytes.Equal(tree.Content.Value, content) {
		t.Errorf("content value = %q, want %q", tree.Content.Value, content)
	}
	if !bytes.Equal(tree.TOC.Value, toc) {
		t.Errorf("toc value = %q, want %q", tree.TOC.Value, toc)
	}

	// Reading a neighboring, never-written fblock must not see this one's
	// bytes — writeFblockV2 must respect fblockOffset(geo, idx).
	if _, err := readFblockV2(backend, geo, 1); err == nil {
		t.Fatalf("expected error reading untouched neighboring fblock, got nil")
	}
}
