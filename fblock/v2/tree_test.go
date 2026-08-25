package v2

import (
	"bytes"
	"testing"
)

func TestAssembleAndReadFblockRoundTrip(t *testing.T) {
	prolog := FixedProlog{
		FormatVersionMajor: 2,
		FormatVersionMinor: 0,
		MaxChannels:        256,
		WriteSequence:      7,
		CatalogTime:        1234567890,
		FblockSize:         1 << 20,
		CatalogEntryCount:  1000,
	}
	params := []byte(`{"fchunk_size":4194304}`)
	catalog := []byte("catalog-bytes")
	content := []byte("content-bytes-opaque-fcontainer")
	toc := []byte("toc-bytes")

	buf, err := AssembleFblock(prolog, params, catalog, content, toc, 64, 0)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}

	tree, err := ReadFblock(buf, 64)
	if err != nil {
		t.Fatalf("ReadFblock: %v", err)
	}

	if tree.Prolog != prolog {
		t.Fatalf("prolog mismatch: got %+v, want %+v", tree.Prolog, prolog)
	}
	if tree.Epilog.Count != MaxEpilogRows {
		t.Fatalf("epilog count = %d, want %d", tree.Epilog.Count, MaxEpilogRows)
	}

	checkNode := func(name string, n Node, wantType NodeType, wantID, wantParent, wantSibling uint32, wantValue []byte) {
		t.Helper()
		if n.Type != wantType {
			t.Errorf("%s: type = %d, want %d", name, n.Type, wantType)
		}
		if n.ID != wantID || n.Parent != wantParent || n.Sibling != wantSibling {
			t.Errorf("%s: id/parent/sibling = %d/%d/%d, want %d/%d/%d", name, n.ID, n.Parent, n.Sibling, wantID, wantParent, wantSibling)
		}
		if !bytes.Equal(n.Value, wantValue) {
			t.Errorf("%s: value = %q, want %q", name, n.Value, wantValue)
		}
	}

	checkNode("root", tree.Root, NodeTypeRoot, 0, 0, 0, nil)
	checkNode("params", tree.Params, NodeTypeParams, 1, 0, 0, params)
	checkNode("catalog", tree.Catalog, NodeTypeCatalog, 2, 0, 1, catalog)
	checkNode("content", tree.Content, NodeTypeContent, 3, 0, 2, content)
	checkNode("toc", tree.TOC, NodeTypeTOC, 4, 0, 3, toc)
}

func TestReadFblockDetectsNodeCorruption(t *testing.T) {
	buf, err := AssembleFblock(FixedProlog{FblockSize: 1 << 20}, []byte("p"), []byte("c"), []byte("content"), []byte("t"), 64, 0)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}

	// Flip a byte inside the content node's value.
	row := findRow(t, buf, NodeTypeContent)
	buf[row.Offset+fixedHeaderSize] ^= 0xFF

	if _, err := ReadFblock(buf, 64); err == nil {
		t.Fatalf("expected error for corrupted node, got nil")
	}
}

func TestAssembleFblockPadsToFixedSize(t *testing.T) {
	const fblockSize = 4096
	buf, err := AssembleFblock(FixedProlog{FblockSize: fblockSize}, []byte("p"), []byte("c"), []byte("content"), []byte("t"), 64, fblockSize)
	if err != nil {
		t.Fatalf("AssembleFblock: %v", err)
	}
	if len(buf) != fblockSize {
		t.Fatalf("len(buf) = %d, want %d", len(buf), fblockSize)
	}

	tree, err := ReadFblock(buf, 64)
	if err != nil {
		t.Fatalf("ReadFblock: %v", err)
	}
	if !bytes.Equal(tree.TOC.Value, []byte("t")) {
		t.Fatalf("toc value = %q, want %q", tree.TOC.Value, "t")
	}

	// The gap between the real end of the last node and the fixed epilog
	// offset is unchecked dead space (ADR-023 §"Решение") — it must exist
	// and be zero-filled (not garbage), since AssembleFblock allocates a
	// fresh zeroed buffer.
	lastRow := findRow(t, buf, NodeTypeTOC)
	gapStart := lastRow.Offset + lastRow.Size
	gapEnd := uint64(fblockSize - EpilogSizeV2)
	if gapStart >= gapEnd {
		t.Fatalf("expected a non-empty gap, got gapStart=%d >= gapEnd=%d", gapStart, gapEnd)
	}
	for i := gapStart; i < gapEnd; i++ {
		if buf[i] != 0 {
			t.Fatalf("gap byte at %d not zero", i)
		}
	}
}

func TestAssembleFblockTooSmallForFixedSize(t *testing.T) {
	_, err := AssembleFblock(FixedProlog{}, nil, nil, []byte("way too much content for this tiny fblock"), nil, 64, 128)
	if err == nil {
		t.Fatalf("expected error when content doesn't fit fblockSize, got nil")
	}
}

// findRow decodes just the epilog to locate a row by type, for tests that
// need to corrupt a specific node's bytes.
func findRow(t *testing.T, buf []byte, nodeType NodeType) EpilogRow {
	t.Helper()
	epilog, err := DecodeEpilog(buf[len(buf)-EpilogSizeV2:])
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	for _, row := range epilog.Rows {
		if row.Type == nodeType {
			return row
		}
	}
	t.Fatalf("no row for node type %d", nodeType)
	return EpilogRow{}
}
