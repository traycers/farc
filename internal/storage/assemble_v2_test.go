package storage

import (
	"bytes"
	"context"
	"hash/crc32"
	"path/filepath"
	"testing"

	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/storageengine"
)

// runEngine starts engine.Run in the background and stops it on test
// cleanup — the same pattern internal/storageengine's own tests use
// (TestRun_DrivesQueuedWork), needed here because writeFblockV2Progressive
// enqueues jobs and blocks on their tickets rather than driving Step
// itself.
func runEngine(t *testing.T, e *storageengine.Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go e.Run(ctx)
	t.Cleanup(cancel)
}

func testEngineConfig() storageengine.Config {
	return storageengine.Config{FchunkSize: 4096, ReadChunkSize: 4096, WarningAt: 100, BackpressureAt: 200, QuotaEvery: 16, QuotaPortions: 4}
}

func TestWriteFblockV2Progressive_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 4, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	engine := storageengine.New(backend, testEngineConfig())
	runEngine(t, engine)

	prolog := fblockv2.FixedProlog{FormatVersionMajor: 2, MaxChannels: geo.MaxChannels, FblockSize: geo.FblockSize, CatalogEntryCount: geo.N}
	params := []byte(`{"fchunk_size":4096}`)
	catalog := []byte("catalog-bytes")
	content := []byte("content-bytes-opaque-fcontainer")
	toc := []byte("toc-bytes")

	if err := writeFblockV2Progressive(engine, geo, 1, backend.Alignment(), prolog, params, catalog, content, toc); err != nil {
		t.Fatalf("writeFblockV2Progressive: %v", err)
	}

	tree, err := readFblockV2(backend, geo, 1)
	if err != nil {
		t.Fatalf("readFblockV2: %v", err)
	}
	if tree.Epilog.Count != fblockv2.MaxEpilogRows {
		t.Fatalf("epilog count = %d, want %d", tree.Epilog.Count, fblockv2.MaxEpilogRows)
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
}

// TestWriteFblockV2Progressive_EpilogGrowsMonotonically proves the point of
// ADR-023's progressive rewrite: a crash between nodes leaves a valid
// epilog whose Count reports exactly how many nodes are confirmed on disk,
// each independently verifiable via its own row CRC -- not just v1.0's
// binary ready/in_progress.
func TestWriteFblockV2Progressive_EpilogGrowsMonotonically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	engine := storageengine.New(backend, testEngineConfig())
	runEngine(t, engine)

	base := int64(fblockOffset(geo, 0))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	alignment := backend.Alignment()

	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize})
	if _, err := engine.EnqueueWrite(base, prologBuf).Wait(); err != nil {
		t.Fatalf("write prolog: %v", err)
	}

	var epilog fblockv2.Epilog
	offset := uint64(len(prologBuf))

	// Simulate a crash after only the root and params nodes.
	for i, n := range []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Value: []byte("params")},
	} {
		if err := writeNodeAndEpilogRow(engine, base, epilogOffset, offset, n, alignment, i, &epilog); err != nil {
			t.Fatalf("writeNodeAndEpilogRow(%d): %v", i, err)
		}
		offset += epilog.Rows[i].Size

		gotEpilogBuf := make([]byte, fblockv2.EpilogSizeV2)
		if _, err := backend.ReadAt(gotEpilogBuf, epilogOffset); err != nil {
			t.Fatalf("read epilog: %v", err)
		}
		got, err := fblockv2.DecodeEpilog(gotEpilogBuf)
		if err != nil {
			t.Fatalf("DecodeEpilog after node %d: %v", i, err)
		}
		if int(got.Count) != i+1 {
			t.Fatalf("after node %d: epilog count = %d, want %d", i, got.Count, i+1)
		}
	}

	// The remaining 3 rows must never have been claimed as valid.
	final := make([]byte, fblockv2.EpilogSizeV2)
	if _, err := backend.ReadAt(final, epilogOffset); err != nil {
		t.Fatalf("read final epilog: %v", err)
	}
	decoded, err := fblockv2.DecodeEpilog(final)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if decoded.Count != 2 {
		t.Fatalf("final epilog count = %d, want 2 (content/toc never written)", decoded.Count)
	}
}

// TestWriteStaticNodesV2 proves promoteLocked's v2.0 building block:
// prolog+root+params+catalog (everything known before content starts)
// written as one combined buffer, with the epilog brought to Count==3 --
// mirroring assembleHeaderAndMagic's one-shot header blob in v1.0.
func TestWriteStaticNodesV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	engine := storageengine.New(backend, testEngineConfig())
	runEngine(t, engine)

	base := int64(fblockOffset(geo, 0))
	prolog := fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N}
	params := []byte(`{"fchunk_size":4096}`)
	catalog := []byte("catalog-bytes")

	epilog, contentOffset, err := writeStaticNodesV2(engine, base, geo.FblockSize, prolog, params, catalog, backend.Alignment())
	if err != nil {
		t.Fatalf("writeStaticNodesV2: %v", err)
	}
	if epilog.Count != 3 {
		t.Fatalf("epilog.Count = %d, want 3", epilog.Count)
	}
	if epilog.Rows[2].Offset+epilog.Rows[2].Size != contentOffset {
		t.Fatalf("contentOffset = %d, want %d (right after catalog row)", contentOffset, epilog.Rows[2].Offset+epilog.Rows[2].Size)
	}

	gotEpilogBuf := make([]byte, fblockv2.EpilogSizeV2)
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	if _, err := backend.ReadAt(gotEpilogBuf, epilogOffset); err != nil {
		t.Fatalf("read epilog: %v", err)
	}
	gotEpilog, err := fblockv2.DecodeEpilog(gotEpilogBuf)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if gotEpilog != epilog {
		t.Fatalf("on-disk epilog = %+v, want %+v", gotEpilog, epilog)
	}

	paramsBuf := make([]byte, epilog.Rows[1].Size)
	if _, err := backend.ReadAt(paramsBuf, base+int64(epilog.Rows[1].Offset)); err != nil {
		t.Fatalf("read params node: %v", err)
	}
	paramsNode, _, err := fblockv2.DecodeNode(paramsBuf)
	if err != nil {
		t.Fatalf("DecodeNode(params): %v", err)
	}
	if !bytes.Equal(paramsNode.Value, params) {
		t.Fatalf("params value = %q, want %q", paramsNode.Value, params)
	}

	catalogBuf := make([]byte, epilog.Rows[2].Size)
	if _, err := backend.ReadAt(catalogBuf, base+int64(epilog.Rows[2].Offset)); err != nil {
		t.Fatalf("read catalog node: %v", err)
	}
	catalogNode, _, err := fblockv2.DecodeNode(catalogBuf)
	if err != nil {
		t.Fatalf("DecodeNode(catalog): %v", err)
	}
	if !bytes.Equal(catalogNode.Value, catalog) {
		t.Fatalf("catalog value = %q, want %q", catalogNode.Value, catalog)
	}
}

// TestStreamedContentNodeWrite proves the open-write integration for a
// node whose value streams in via Append rather than being known upfront
// (today, only content) — its header (which needs the final value_size)
// and trailer are deferred to close, exactly as ADR-023 §"Решение"
// requires.
func TestStreamedContentNodeWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	engine := storageengine.New(backend, testEngineConfig())
	runEngine(t, engine)

	base := int64(fblockOffset(geo, 0))
	const nodeOffset = 128

	w := openNodeWriterV2(engine, base, nodeOffset, fblockv2.NodeTypeContent, 3, 0, 2, 0)
	var full []byte
	for _, chunk := range [][]byte{[]byte("hello "), []byte("streamed "), []byte("content")} {
		if err := w.Append(chunk); err != nil {
			t.Fatalf("Append: %v", err)
		}
		full = append(full, chunk...)
	}

	row, err := w.close(backend.Alignment())
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	if row.Type != fblockv2.NodeTypeContent || row.ID != 3 {
		t.Fatalf("row type/id = %d/%d, want %d/%d", row.Type, row.ID, fblockv2.NodeTypeContent, 3)
	}
	wantSize := uint64(fblockv2.NodeTotalSize(int64(len(full)), backend.Alignment()))
	if row.Size != wantSize {
		t.Fatalf("row.Size = %d, want %d", row.Size, wantSize)
	}
	if row.CRC32 != crc32.ChecksumIEEE(full) {
		t.Fatalf("row.CRC32 mismatch")
	}

	nodeBuf := make([]byte, row.Size)
	if _, err := backend.ReadAt(nodeBuf, base+int64(nodeOffset)); err != nil {
		t.Fatalf("read node: %v", err)
	}
	decoded, consumed, err := fblockv2.DecodeNode(nodeBuf)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	if consumed != len(nodeBuf) || !bytes.Equal(decoded.Value, full) {
		t.Fatalf("decode mismatch: consumed=%d value=%q", consumed, decoded.Value)
	}
	if decoded.Parent != 0 || decoded.Sibling != 2 {
		t.Fatalf("parent/sibling = %d/%d, want 0/2", decoded.Parent, decoded.Sibling)
	}
}
