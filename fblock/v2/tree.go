package v2

import (
	"fmt"
	"hash/crc32"
)

// Tree is the decoded set of top-level nodes plus the fixed prolog and
// epilog directory that frame them (§12.2, §12.4, §12.5).
type Tree struct {
	Prolog  FixedProlog
	Root    Node
	Params  Node
	Catalog Node
	Content Node
	TOC     Node
	Epilog  Epilog
}

// AssembleFblock builds a complete v2.0 fblock (§12.1): the fixed prolog,
// followed by the root/params/catalog/content/toc nodes in that order (each
// padded to alignment), followed by the epilog directory (§12.5) with
// Count=MaxEpilogRows — a one-shot assembly, not the progressive per-node
// epilog rewrite used by the real write path (ADR-023 §"Решение"). Epilog
// row offsets are absolute from the start of the returned buffer.
//
// padToSize, when non-zero, pads the returned buffer to exactly that many
// bytes by moving the epilog to the fixed offset padToSize-EpilogSizeV2,
// leaving an unchecked, zero-filled gap between the real end of the last
// node and the epilog — the real on-disk shape of a fblock, whose epilog
// must sit at a fixed, scan-free offset regardless of how much of its
// capacity the nodes actually use (ADR-023 §"Решение": v2.0 no longer pays
// v1.0's cost of zero-padding Content itself to reach that same guarantee).
// padToSize==0 tight-packs the epilog right after the last node instead
// (used by tests/callers that don't care about an outer fblock_size).
func AssembleFblock(prolog FixedProlog, params, catalog, content, toc []byte, alignment int, padToSize uint64) ([]byte, error) {
	nodes := []Node{
		{Type: NodeTypeRoot, ID: 0, Parent: 0, Sibling: 0},
		{Type: NodeTypeParams, ID: 1, Parent: 0, Sibling: 0, Value: params},
		{Type: NodeTypeCatalog, ID: 2, Parent: 0, Sibling: 1, Value: catalog},
		{Type: NodeTypeContent, ID: 3, Parent: 0, Sibling: 2, Value: content},
		{Type: NodeTypeTOC, ID: 4, Parent: 0, Sibling: 3, Value: toc},
	}

	body := EncodeFixedProlog(prolog)
	var epilog Epilog
	epilog.Count = MaxEpilogRows

	for i, n := range nodes {
		encoded, err := EncodeNode(n, alignment)
		if err != nil {
			return nil, fmt.Errorf("fblock/v2: assemble: %w", err)
		}
		epilog.Rows[i] = EpilogRow{
			Type:   n.Type,
			ID:     n.ID,
			Offset: uint64(len(body)),
			Size:   uint64(len(encoded)),
			CRC32:  crc32.ChecksumIEEE(n.Value),
		}
		body = append(body, encoded...)
	}

	if padToSize > 0 {
		if padToSize < uint64(EpilogSizeV2) || uint64(len(body)) > padToSize-uint64(EpilogSizeV2) {
			return nil, fmt.Errorf("fblock/v2: assemble: nodes (%d bytes) don't fit before fixed epilog offset %d (padToSize=%d)", len(body), int64(padToSize)-int64(EpilogSizeV2), padToSize)
		}
		epilogStart := padToSize - uint64(EpilogSizeV2)
		body = append(body, make([]byte, epilogStart-uint64(len(body)))...)
	}

	body = append(body, EncodeEpilog(epilog)...)
	return body, nil
}

// ReadFblock parses a buffer produced by AssembleFblock: decodes the
// trailing epilog directory, then uses its rows to locate and decode each
// of the MaxEpilogRows nodes directly — no forward scanning.
func ReadFblock(buf []byte, alignment int) (Tree, error) {
	if len(buf) < EpilogSizeV2 {
		return Tree{}, fmt.Errorf("fblock/v2: buffer too short for epilog: %d < %d", len(buf), EpilogSizeV2)
	}
	prolog, err := DecodeFixedProlog(buf)
	if err != nil {
		return Tree{}, fmt.Errorf("fblock/v2: read fblock: %w", err)
	}
	epilog, err := DecodeEpilog(buf[len(buf)-EpilogSizeV2:])
	if err != nil {
		return Tree{}, fmt.Errorf("fblock/v2: read fblock: %w", err)
	}
	if epilog.Count != MaxEpilogRows {
		return Tree{}, fmt.Errorf("fblock/v2: read fblock: epilog count = %d, want %d (fblock not fully assembled)", epilog.Count, MaxEpilogRows)
	}

	tree := Tree{Prolog: prolog, Epilog: epilog}
	for _, row := range epilog.Rows {
		if row.Offset+row.Size > uint64(len(buf)) {
			return Tree{}, fmt.Errorf("fblock/v2: read fblock: row for node id=%d out of bounds", row.ID)
		}
		nodeBuf := buf[row.Offset : row.Offset+row.Size]
		n, _, err := DecodeNode(nodeBuf)
		if err != nil {
			return Tree{}, fmt.Errorf("fblock/v2: read fblock: node id=%d: %w", row.ID, err)
		}
		// row.CRC32 mirrors the node's own crc32_value (§12.5) — DecodeNode
		// already verified it once inside the node; this catches corruption
		// of the epilog's own copy specifically.
		if crc32.ChecksumIEEE(n.Value) != row.CRC32 {
			return Tree{}, fmt.Errorf("fblock/v2: read fblock: epilog crc32 mismatch for node id=%d", row.ID)
		}

		switch n.Type {
		case NodeTypeRoot:
			tree.Root = n
		case NodeTypeParams:
			tree.Params = n
		case NodeTypeCatalog:
			tree.Catalog = n
		case NodeTypeContent:
			tree.Content = n
		case NodeTypeTOC:
			tree.TOC = n
		default:
			return Tree{}, fmt.Errorf("fblock/v2: read fblock: unknown node type %d for id=%d", n.Type, n.ID)
		}
	}
	return tree, nil
}
