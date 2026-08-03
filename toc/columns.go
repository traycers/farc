// Package toc implements the fcontainer TOC section: a Structure-of-Arrays
// index over the Content tree, built once at fblock finalization
// (docs/docs/archive/06-toc-format.md).
//
// This package depends on mediatree for the base element vocabulary
// (NodeType, Role, Element) — the same closed/open type/role sets Content
// uses (docs/docs/archive/05-data-format.md §3, 07-media-tree.md §3.1).
// Because mediatree's own domain-level query helpers would otherwise need to
// import toc back, those helpers live one layer up (e.g. in the Reader code
// that has both packages available) rather than inside package mediatree
// itself, to keep this dependency a DAG.
package toc

import (
	"encoding/binary"
	"fmt"

	"traycers/farc/mediatree"
)

// Align is TOC_ALIGN: every column starts at a multiple of this many bytes
// from the start of the TOC section (docs/docs/archive/06-toc-format.md §3).
const Align = 64

// HeaderSize is the fixed TOC header block: exactly one Align-sized block.
const HeaderSize = Align

// Columns is the TOC's Structure-of-Arrays representation. Row index = a
// node's id AFTER DFS reordering (§5) — NOT its Content creation-order id.
type Columns struct {
	VersionMajor uint16
	VersionMinor uint16
	N            uint32

	Type    []mediatree.NodeType // len N
	Role    []mediatree.Role     // len N
	Parent  []uint32             // len N, post-reorder ids
	Sibling []uint32             // len N, post-reorder ids; kept even
	// though DFS makes it redundant (08-array-trees.md §8.4) — it's the only
	// documented way to find a GOP's keyframe (07-media-tree.md §6).
	ValueOrOffset []uint64 // len N, tagged union keyed by Type[i], see §4.1
	Size          []uint64 // len N, meaningful only for bytes/string types
}

// columnWidths in on-disk column order (docs/docs/archive/06-toc-format.md §4).
var columnWidths = [6]int{1, 2, 4, 4, 8, 8}

// Offsets holds each column's byte offset within the TOC section.
type Offsets struct {
	Type, Role, Parent, Sibling, ValueOrOffset, Size uint32
	Total                                            uint32
}

func pad(n uint32, width int) uint32 {
	size := n * uint32(width)
	r := size % Align
	if r == 0 {
		return 0
	}
	return Align - r
}

// ComputeOffsets computes the six column offsets for n rows. The last
// column (Size) is never padded — nothing follows it in the TOC section.
func ComputeOffsets(n uint32) Offsets {
	var o Offsets
	off := uint32(HeaderSize)
	o.Type = off
	off += n*uint32(columnWidths[0]) + pad(n, columnWidths[0])
	o.Role = off
	off += n*uint32(columnWidths[1]) + pad(n, columnWidths[1])
	o.Parent = off
	off += n*uint32(columnWidths[2]) + pad(n, columnWidths[2])
	o.Sibling = off
	off += n*uint32(columnWidths[3]) + pad(n, columnWidths[3])
	o.ValueOrOffset = off
	off += n*uint32(columnWidths[4]) + pad(n, columnWidths[4])
	o.Size = off
	off += n * uint32(columnWidths[5]) // unpadded
	o.Total = off
	return o
}

func validateColumnsShape(c *Columns) error {
	n := int(c.N)
	switch {
	case len(c.Type) != n:
		return fmt.Errorf("toc: Type len %d != N %d", len(c.Type), n)
	case len(c.Role) != n:
		return fmt.Errorf("toc: Role len %d != N %d", len(c.Role), n)
	case len(c.Parent) != n:
		return fmt.Errorf("toc: Parent len %d != N %d", len(c.Parent), n)
	case len(c.Sibling) != n:
		return fmt.Errorf("toc: Sibling len %d != N %d", len(c.Sibling), n)
	case len(c.ValueOrOffset) != n:
		return fmt.Errorf("toc: ValueOrOffset len %d != N %d", len(c.ValueOrOffset), n)
	case len(c.Size) != n:
		return fmt.Errorf("toc: Size len %d != N %d", len(c.Size), n)
	}
	return nil
}

// Encode serializes c per the exact column layout and padding rule in
// docs/docs/archive/06-toc-format.md §3-4.
func Encode(c *Columns) ([]byte, error) {
	if err := validateColumnsShape(c); err != nil {
		return nil, err
	}
	offs := ComputeOffsets(c.N)
	buf := make([]byte, offs.Total)

	binary.LittleEndian.PutUint16(buf[0:2], c.VersionMajor)
	binary.LittleEndian.PutUint16(buf[2:4], c.VersionMinor)
	binary.LittleEndian.PutUint32(buf[4:8], c.N)
	// buf[8:64] reserved, zero

	for i := uint32(0); i < c.N; i++ {
		buf[offs.Type+i] = uint8(c.Type[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint16(buf[offs.Role+i*2:offs.Role+i*2+2], uint16(c.Role[i]))
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint32(buf[offs.Parent+i*4:offs.Parent+i*4+4], c.Parent[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint32(buf[offs.Sibling+i*4:offs.Sibling+i*4+4], c.Sibling[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint64(buf[offs.ValueOrOffset+i*8:offs.ValueOrOffset+i*8+8], c.ValueOrOffset[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint64(buf[offs.Size+i*8:offs.Size+i*8+8], c.Size[i])
	}
	return buf, nil
}

// Decode parses a Columns from buf. The TOC major version gates
// compatibility (docs/docs/archive/06-toc-format.md §3.1): an incompatible
// major version means the reader must refuse to parse the TOC at all — it
// does not know the column layout. Decode itself always assumes the current
// (v1) layout; callers must check VersionMajor before trusting the result if
// multiple major versions may ever be on disk.
func Decode(buf []byte) (*Columns, error) {
	if len(buf) < HeaderSize {
		return nil, fmt.Errorf("toc: buffer too short for header: %d < %d", len(buf), HeaderSize)
	}
	c := &Columns{
		VersionMajor: binary.LittleEndian.Uint16(buf[0:2]),
		VersionMinor: binary.LittleEndian.Uint16(buf[2:4]),
		N:            binary.LittleEndian.Uint32(buf[4:8]),
	}
	offs := ComputeOffsets(c.N)
	if uint32(len(buf)) < offs.Total {
		return nil, fmt.Errorf("toc: buffer too short: %d < %d (N=%d)", len(buf), offs.Total, c.N)
	}

	c.Type = make([]mediatree.NodeType, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Type[i] = mediatree.NodeType(buf[offs.Type+i])
	}
	c.Role = make([]mediatree.Role, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Role[i] = mediatree.Role(binary.LittleEndian.Uint16(buf[offs.Role+i*2 : offs.Role+i*2+2]))
	}
	c.Parent = make([]uint32, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Parent[i] = binary.LittleEndian.Uint32(buf[offs.Parent+i*4 : offs.Parent+i*4+4])
	}
	c.Sibling = make([]uint32, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Sibling[i] = binary.LittleEndian.Uint32(buf[offs.Sibling+i*4 : offs.Sibling+i*4+4])
	}
	c.ValueOrOffset = make([]uint64, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.ValueOrOffset[i] = binary.LittleEndian.Uint64(buf[offs.ValueOrOffset+i*8 : offs.ValueOrOffset+i*8+8])
	}
	c.Size = make([]uint64, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Size[i] = binary.LittleEndian.Uint64(buf[offs.Size+i*8 : offs.Size+i*8+8])
	}
	return c, nil
}
