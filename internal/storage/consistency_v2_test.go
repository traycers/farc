package storage

import (
	"encoding/binary"
	"hash/crc32"
	"path/filepath"
	"testing"
	"time"

	"github.com/traycers/farc/fblock"
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/storageengine"
	"github.com/traycers/farc/mediatree"
)

// TestVerifyWriteCompletionV2_FullyWritten proves a fully-assembled v2.0
// fblock (Count==MaxEpilogRows, every node decoding cleanly) resolves as
// complete — the v2.0 analog of v1.0's WriteComplete outcome
// (verifyWriteCompletion), but derived from the progressive epilog rather
// than a single binary epilog CRC.
func TestVerifyWriteCompletionV2_FullyWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	prolog := fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N}
	buf, err := fblockv2.AssembleFblock(prolog, []byte("params"), []byte("catalog"), []byte("content"), []byte("toc"), backend.Alignment(), geo.FblockSize)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}
	if _, err := backend.WriteAt(buf, int64(fblockOffset(geo, 0))); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	epilog, complete, err := verifyWriteCompletionV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2: %v", err)
	}
	if !complete {
		t.Fatalf("complete = false, want true (epilog=%+v)", epilog)
	}
	if epilog.Count != fblockv2.MaxEpilogRows {
		t.Fatalf("epilog.Count = %d, want %d", epilog.Count, fblockv2.MaxEpilogRows)
	}
}

// TestVerifyWriteCompletionV2_PartialCount proves a fblock crashed midway
// (only some nodes confirmed by the progressive epilog) resolves as
// incomplete, and reports exactly how many nodes are confirmed --
// information v1.0 never had (only a binary ready/in_progress).
func TestVerifyWriteCompletionV2_PartialCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	base := int64(fblockOffset(geo, 0))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	alignment := backend.Alignment()

	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize})
	if _, err := backend.WriteAt(prologBuf, base); err != nil {
		t.Fatalf("write prolog: %v", err)
	}

	var epilog fblockv2.Epilog
	offset := uint64(len(prologBuf))
	for i, n := range []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Value: []byte("params")},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Value: []byte("catalog")},
	} {
		encoded, err := fblockv2.EncodeNode(n, alignment)
		if err != nil {
			t.Fatalf("EncodeNode: %v", err)
		}
		if _, err := backend.WriteAt(encoded, base+int64(offset)); err != nil {
			t.Fatalf("write node %d: %v", i, err)
		}
		epilog.Rows[i] = fblockv2.EpilogRow{Type: n.Type, ID: n.ID, Offset: offset, Size: uint64(len(encoded))}
		epilog.Count++
		if _, err := backend.WriteAt(fblockv2.EncodeEpilog(epilog), epilogOffset); err != nil {
			t.Fatalf("write epilog after node %d: %v", i, err)
		}
		offset += uint64(len(encoded))
	}
	// content/toc never written -- simulates a crash right after catalog.

	gotEpilog, complete, err := verifyWriteCompletionV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2: %v", err)
	}
	if complete {
		t.Fatalf("complete = true, want false")
	}
	if gotEpilog.Count != 3 {
		t.Fatalf("epilog.Count = %d, want 3", gotEpilog.Count)
	}
}

// TestVerifyWriteCompletionV2_NeverWritten proves an untouched (all-zero)
// fblock slot resolves as incomplete rather than erroring -- the v2.0
// analog of ADR-006's uninitialized detection at the epilog end.
func TestVerifyWriteCompletionV2_NeverWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	epilog, complete, err := verifyWriteCompletionV2(backend, geo, 1)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2: %v", err)
	}
	if complete {
		t.Fatalf("complete = true, want false")
	}
	if epilog.Count != 0 {
		t.Fatalf("epilog.Count = %d, want 0", epilog.Count)
	}
}

// TestVerifyWriteCompletionV2_CorruptedNodeAfterFullCount proves that even
// with Count==MaxEpilogRows, a node whose own bytes are corrupted on disk
// (bit rot after the write, not a crash during it) is caught -- the epilog
// row's crc32 (a copy of the node's own crc32_value) plus the node's own
// crc32_header/crc32_value give two independent chances to catch it.
func TestVerifyWriteCompletionV2_CorruptedNodeAfterFullCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 4096, N: 2, MaxChannels: 64}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))

	prolog := fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize}
	buf, err := fblockv2.AssembleFblock(prolog, []byte("params"), []byte("catalog"), []byte("content"), []byte("toc"), backend.Alignment(), geo.FblockSize)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}
	// Flip a byte inside the catalog node's value (somewhere past the fixed
	// prolog + root + params nodes -- exact offset doesn't matter, any byte
	// in the tree region does).
	buf[200] ^= 0xFF
	if _, err := backend.WriteAt(buf, int64(fblockOffset(geo, 0))); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	_, complete, err := verifyWriteCompletionV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2: %v", err)
	}
	if complete {
		t.Fatalf("complete = true, want false (corrupted node)")
	}
}

// TestRecoverPartialWriteV2_ContentStillOpen simulates a crash while
// content is mid-write (epilog.Count==3: root/params/catalog confirmed,
// content/toc not) with one periodic-flush trigger's MagicTrailer already
// landed (ADR-017, unchanged in v2.0) -- recoverPartialWriteV2 must
// recover the confirmed frames, finalize content's node, build and write
// TOC, and bring the epilog to Count==5.
func TestRecoverPartialWriteV2_ContentStillOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 8192, N: 2, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	alignment := backend.Alignment()

	base := int64(fblockOffset(geo, 0))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)

	wantUUID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	cat := fblock.NewCatalog(geo.MaxChannels, geo.N)
	cat.UUID[0] = wantUUID
	catalogBuf, err := fblock.EncodeCatalog(cat)
	if err != nil {
		t.Fatalf("EncodeCatalog: %v", err)
	}

	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{
		FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N,
	})
	if _, err := backend.WriteAt(prologBuf, base); err != nil {
		t.Fatalf("write prolog: %v", err)
	}

	var epilog fblockv2.Epilog
	offset := uint64(len(prologBuf))
	for i, n := range []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Value: []byte(`{"fchunk_size":4096}`)},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Value: catalogBuf},
	} {
		encoded, err := fblockv2.EncodeNode(n, alignment)
		if err != nil {
			t.Fatalf("EncodeNode: %v", err)
		}
		if _, err := backend.WriteAt(encoded, base+int64(offset)); err != nil {
			t.Fatalf("write node %d: %v", i, err)
		}
		epilog.Rows[i] = fblockv2.EpilogRow{Type: n.Type, ID: n.ID, Offset: offset, Size: uint64(len(encoded)), CRC32: 0}
		epilog.Count++
		if _, err := backend.WriteAt(fblockv2.EncodeEpilog(epilog), epilogOffset); err != nil {
			t.Fatalf("write epilog after node %d: %v", i, err)
		}
		offset += uint64(len(encoded))
	}

	// Content is "open": one confirmed frame followed by a live MagicTrailer,
	// written directly into the value region (header not written yet).
	contentOffset := offset
	valueRegionStart := base + int64(contentOffset) + int64(fblockv2.FixedHeaderSize)

	wantTimestamp := uint64(123456789)
	tsValue := make([]byte, 8)
	binary.LittleEndian.PutUint64(tsValue, wantTimestamp)
	rawContent := append(
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0}),
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeTimestamp, Role: mediatree.RoleFrameTimeVideo, Parent: 0, Sibling: 1, Value: tsValue})...,
	)
	trailer := fblock.EncodeTrailer(alignment)
	if _, err := backend.WriteAt(append(rawContent, trailer...), valueRegionStart); err != nil {
		t.Fatalf("write raw content+trailer: %v", err)
	}

	gotUUID, begin, end, ok, err := recoverPartialWriteV2(backend, geo, 0, alignment)
	if err != nil {
		t.Fatalf("recoverPartialWriteV2: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if gotUUID != wantUUID {
		t.Fatalf("uuid = %x, want %x", gotUUID, wantUUID)
	}
	if begin != wantTimestamp || end != wantTimestamp {
		t.Fatalf("begin/end = %d/%d, want %d/%d", begin, end, wantTimestamp, wantTimestamp)
	}

	epilogAfter, complete, err := verifyWriteCompletionV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2 after recovery: %v", err)
	}
	if !complete {
		t.Fatalf("complete = false after recovery, epilog=%+v", epilogAfter)
	}
}

// TestRecoverPartialWriteV2_ContentStillOpen_AlignmentEnforcingBackend is
// TestRecoverPartialWriteV2_ContentStillOpen's real-`direct`-backend
// counterpart: every write here (static nodes, the open content job's own
// periodic flush) goes through a genuine storageengine.Engine over a
// backend that rejects any misaligned WriteAt exactly like
// internal/ioengine's DirectBackend does, so the fixture itself proves the
// content region's own writes land aligned -- not just that recovery's
// read-side math is right (every other recovery test in this file runs on
// openStandard, Alignment()==1, which can't exercise this at all).
func TestRecoverPartialWriteV2_ContentStillOpen_AlignmentEnforcingBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 8192, N: 2, MaxChannels: 4}
	plain := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	aligned := &alignmentEnforcingBackend{Backend: plain, align: 8}
	alignment := aligned.Alignment()

	engine := storageengine.New(aligned, storageengine.Config{FchunkSize: 8, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})
	runEngine(t, engine)

	base := int64(fblockOffset(geo, 0))

	wantUUID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	cat := fblock.NewCatalog(geo.MaxChannels, geo.N)
	cat.UUID[0] = wantUUID
	catalogBuf, err := fblock.EncodeCatalog(cat)
	if err != nil {
		t.Fatalf("EncodeCatalog: %v", err)
	}
	paramsBuf := []byte(`{"fchunk_size":4096}`)

	prolog := fblockv2.FixedProlog{FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N}
	_, contentOffset, err := writeStaticNodesV2(engine, base, geo.FblockSize, prolog, paramsBuf, catalogBuf, alignment)
	if err != nil {
		t.Fatalf("writeStaticNodesV2: %v", err)
	}

	wantTimestamp := uint64(123456789)
	tsValue := make([]byte, 8)
	binary.LittleEndian.PutUint64(tsValue, wantTimestamp)
	rawContent := append(
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0}),
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeTimestamp, Role: mediatree.RoleFrameTimeVideo, Parent: 0, Sibling: 1, Value: tsValue})...,
	)
	// Pad to a multiple of alignment so the periodic flush confirms every
	// byte of it without needing more data or a Close -- applyFlushLocked
	// only ever flushes whole alignment units (internal/storageengine/
	// engine.go), same as production; the trailing zero bytes don't form a
	// full element, so DecodeContentPartial simply stops there, same as if
	// the stream had been cut off at that exact point.
	if rem := len(rawContent) % alignment; rem != 0 {
		rawContent = append(rawContent, make([]byte, alignment-rem)...)
	}

	valueBase := base + int64(contentOffset) + int64(fblockv2.FixedHeaderSize)
	handle := engine.EnqueueOpenWrite(valueBase, nil, 0)
	if err := handle.Append(rawContent); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// No Close -- the "crash" is exactly whatever the periodic flush
	// (fchunk_size==alignment==8, so rawContent's own bytes trigger it)
	// has confirmed on disk by itself.
	deadline := time.Now().Add(5 * time.Second)
	for handle.Written() < int64(len(rawContent)) {
		if time.Now().After(deadline) {
			t.Fatalf("Written() = %d, want >= %d (flush never confirmed)", handle.Written(), len(rawContent))
		}
		time.Sleep(time.Millisecond)
	}

	gotUUID, begin, end, ok, err := recoverPartialWriteV2(aligned, geo, 0, alignment)
	if err != nil {
		t.Fatalf("recoverPartialWriteV2: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if gotUUID != wantUUID {
		t.Fatalf("uuid = %x, want %x", gotUUID, wantUUID)
	}
	if begin != wantTimestamp || end != wantTimestamp {
		t.Fatalf("begin/end = %d/%d, want %d/%d", begin, end, wantTimestamp, wantTimestamp)
	}

	epilogAfter, complete, err := verifyWriteCompletionV2(aligned, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2 after recovery: %v", err)
	}
	if !complete {
		t.Fatalf("complete = false after recovery, epilog=%+v", epilogAfter)
	}
}

// TestRecoverPartialWriteV2_ContentCompleteTOCMissing simulates a crash
// right after content's own node finished closing (its header/tail
// written, epilog.Count==4) but before TOC — a simpler case than content
// still being open: no MagicTrailer search needed, content decodes in
// full.
func TestRecoverPartialWriteV2_ContentCompleteTOCMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "storage.img")
	geo := Geometry{FblockSize: 8192, N: 2, MaxChannels: 4}
	backend := openStandard(t, path, int64(geo.FblockSize)*int64(geo.N))
	alignment := backend.Alignment()

	base := int64(fblockOffset(geo, 0))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)

	wantUUID := [16]byte{9, 8, 7, 6, 5, 4, 3, 2, 1}
	cat := fblock.NewCatalog(geo.MaxChannels, geo.N)
	cat.UUID[0] = wantUUID
	catalogBuf, err := fblock.EncodeCatalog(cat)
	if err != nil {
		t.Fatalf("EncodeCatalog: %v", err)
	}

	wantTimestamp := uint64(42424242)
	tsValue := make([]byte, 8)
	binary.LittleEndian.PutUint64(tsValue, wantTimestamp)
	contentValue := append(
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0}),
		mediatree.EncodeElement(mediatree.Element{Type: mediatree.TypeTimestamp, Role: mediatree.RoleFrameTimeVideo, Parent: 0, Sibling: 1, Value: tsValue})...,
	)

	var epilog fblockv2.Epilog
	prologBuf := fblockv2.EncodeFixedProlog(fblockv2.FixedProlog{
		FormatVersionMajor: 2, FblockSize: geo.FblockSize, MaxChannels: geo.MaxChannels, CatalogEntryCount: geo.N,
	})
	if _, err := backend.WriteAt(prologBuf, base); err != nil {
		t.Fatalf("write prolog: %v", err)
	}
	offset := uint64(len(prologBuf))
	for i, n := range []fblockv2.Node{
		{Type: fblockv2.NodeTypeRoot, ID: 0},
		{Type: fblockv2.NodeTypeParams, ID: 1, Value: []byte("params")},
		{Type: fblockv2.NodeTypeCatalog, ID: 2, Value: catalogBuf},
		{Type: fblockv2.NodeTypeContent, ID: 3, Parent: 0, Sibling: 2, Value: contentValue},
	} {
		encoded, err := fblockv2.EncodeNode(n, alignment)
		if err != nil {
			t.Fatalf("EncodeNode: %v", err)
		}
		if _, err := backend.WriteAt(encoded, base+int64(offset)); err != nil {
			t.Fatalf("write node %d: %v", i, err)
		}
		epilog.Rows[i] = fblockv2.EpilogRow{Type: n.Type, ID: n.ID, Offset: offset, Size: uint64(len(encoded)), CRC32: crc32.ChecksumIEEE(n.Value)}
		epilog.Count++
		if _, err := backend.WriteAt(fblockv2.EncodeEpilog(epilog), epilogOffset); err != nil {
			t.Fatalf("write epilog after node %d: %v", i, err)
		}
		offset += uint64(len(encoded))
	}
	// toc never written -- simulates a crash right after content closed.

	gotUUID, begin, end, ok, err := recoverPartialWriteV2(backend, geo, 0, alignment)
	if err != nil {
		t.Fatalf("recoverPartialWriteV2: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if gotUUID != wantUUID {
		t.Fatalf("uuid = %x, want %x", gotUUID, wantUUID)
	}
	if begin != wantTimestamp || end != wantTimestamp {
		t.Fatalf("begin/end = %d/%d, want %d/%d", begin, end, wantTimestamp, wantTimestamp)
	}

	epilogAfter, complete, err := verifyWriteCompletionV2(backend, geo, 0)
	if err != nil {
		t.Fatalf("verifyWriteCompletionV2 after recovery: %v", err)
	}
	if !complete {
		t.Fatalf("complete = false after recovery, epilog=%+v", epilogAfter)
	}
}
