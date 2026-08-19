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

	"github.com/traycers/farc/mediatree"
)

// Align is TOC_ALIGN: every column starts at a multiple of this many bytes
// from the start of the TOC section (docs/docs/archive/06-toc-format.md §3).
const Align = 64

// HeaderSize is the fixed TOC header block: exactly one Align-sized block.
const HeaderSize = Align

// dirEntrySize is the on-disk width of one column-directory entry: kind(2) +
// width(1) + reserved(1) (docs/docs/archive/06-toc-format.md §3, column
// directory).
const dirEntrySize = 4

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

// ColumnKind identifies a TOC column's on-disk meaning, declared in the
// column directory that follows the header (docs/docs/archive/
// 06-toc-format.md §3-4). Open, append-only forever — the same discipline as
// mediatree.Role (07-media-tree.md §3.1) and TOC's own major/minor version: a
// code, once written to disk, is never renumbered or reused.
type ColumnKind uint16

const (
	ColumnKindType          ColumnKind = 0
	ColumnKindRole          ColumnKind = 1
	ColumnKindParent        ColumnKind = 2
	ColumnKindSibling       ColumnKind = 3
	ColumnKindValueOrOffset ColumnKind = 4
	ColumnKindSize          ColumnKind = 5
)

// knownColumnWidth returns kind's expected element width in bytes, for the
// columns this build understands — mirrors mediatree.NodeType.FixedSize().
// ok is false for a kind this build doesn't recognize (e.g. written by a
// newer minor version); callers must still use the on-disk dirEntry's own
// Width to skip such a column safely, never fabricate a width from this
// table.
func knownColumnWidth(kind ColumnKind) (width uint8, ok bool) {
	switch kind {
	case ColumnKindType:
		return 1, true
	case ColumnKindRole:
		return 2, true
	case ColumnKindParent:
		return 4, true
	case ColumnKindSibling:
		return 4, true
	case ColumnKindValueOrOffset:
		return 8, true
	case ColumnKindSize:
		return 8, true
	default:
		return 0, false
	}
}

// dirEntry is one parsed column-directory entry: which column, and how wide
// each of its n elements is.
type dirEntry struct {
	Kind  ColumnKind
	Width uint8
}

// canonicalColumns is the fixed 6-entry directory Encode always writes, in
// on-disk order (docs/docs/archive/06-toc-format.md §4). Unexported:
// mutating it in place would corrupt every subsequent Encode call.
var canonicalColumns = []dirEntry{
	{ColumnKindType, 1},
	{ColumnKindRole, 2},
	{ColumnKindParent, 4},
	{ColumnKindSibling, 4},
	{ColumnKindValueOrOffset, 8},
	{ColumnKindSize, 8},
}

// EncodedSize returns the exact byte size Encode would produce for a
// Columns with n elements, without building or encoding one -- the
// closed-form function of element count alone that backs the
// pool-status-list's live TOC-size figure while a fblock is still filling
// (.scratch/fblocks-ui/spec.md item 12): 128-byte fixed header+directory,
// 27 bytes/element, plus bounded per-column alignment padding.
func EncodedSize(n uint32) uint32 {
	_, total := columnOffsets(canonicalColumns, n)
	return total
}

func pad(n uint32, width int) uint32 {
	size := n * uint32(width)
	r := size % Align
	if r == 0 {
		return 0
	}
	return Align - r
}

// columnOffsets computes, for a column directory (in its own on-disk order)
// and n rows, each entry's data start offset (parallel to entries) plus the
// section's total encoded size. Generic over both the canonical 6-entry
// directory Encode writes and whatever directory Decode actually finds on
// disk — including a hypothetical trailing entry of an unrecognized kind,
// whose declared Width still has to shift every later column's offset.
// Every entry's data block, including the last, is padded to Align
// (docs/docs/archive/06-toc-format.md §3) — no special case for "the last
// column", so the total is always a multiple of Align.
func columnOffsets(entries []dirEntry, n uint32) (offsets []uint32, total uint32) {
	dirBytes := uint32(len(entries)) * dirEntrySize
	off := uint32(HeaderSize) + dirBytes + pad(uint32(len(entries)), dirEntrySize)

	offsets = make([]uint32, len(entries))
	for i, e := range entries {
		offsets[i] = off
		off += n*uint32(e.Width) + pad(n, int(e.Width))
	}
	return offsets, off
}

// findColumn locates the single directory entry for kind, validating its
// declared width against knownColumnWidth. Errors if kind is missing or
// duplicated — a required column must appear exactly once (defends against a
// corrupted or maliciously crafted directory).
func findColumn(entries []dirEntry, kind ColumnKind) (idx int, err error) {
	idx = -1
	for i, e := range entries {
		if e.Kind != kind {
			continue
		}
		if idx != -1 {
			return -1, fmt.Errorf("toc: duplicate column directory entry for kind %d", kind)
		}
		idx = i
	}
	if idx == -1 {
		return -1, fmt.Errorf("toc: column directory missing required kind %d", kind)
	}
	wantWidth, _ := knownColumnWidth(kind) // kind is always one of the required, known ones here
	if entries[idx].Width != wantWidth {
		return -1, fmt.Errorf("toc: column kind %d has width %d, want %d", kind, entries[idx].Width, wantWidth)
	}
	return idx, nil
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

// Encode serializes c per the exact header/directory/column layout and
// padding rule in docs/docs/archive/06-toc-format.md §3-4.
func Encode(c *Columns) ([]byte, error) {
	err := validateColumnsShape(c)
	if err != nil {
		return nil, err
	}
	offsets, total := columnOffsets(canonicalColumns, c.N)
	buf := make([]byte, total)

	binary.LittleEndian.PutUint16(buf[0:2], c.VersionMajor)
	binary.LittleEndian.PutUint16(buf[2:4], c.VersionMinor)
	binary.LittleEndian.PutUint32(buf[4:8], c.N)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(len(canonicalColumns)))
	// buf[10:64] reserved, zero

	for i, e := range canonicalColumns {
		base := uint32(HeaderSize) + uint32(i)*dirEntrySize
		binary.LittleEndian.PutUint16(buf[base:base+2], uint16(e.Kind))
		buf[base+2] = e.Width
		// buf[base+3] reserved, zero
	}

	offType, offRole, offParent := offsets[0], offsets[1], offsets[2]
	offSibling, offValueOrOffset, offSize := offsets[3], offsets[4], offsets[5]

	for i := uint32(0); i < c.N; i++ {
		buf[offType+i] = uint8(c.Type[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint16(buf[offRole+i*2:offRole+i*2+2], uint16(c.Role[i]))
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint32(buf[offParent+i*4:offParent+i*4+4], c.Parent[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint32(buf[offSibling+i*4:offSibling+i*4+4], c.Sibling[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint64(buf[offValueOrOffset+i*8:offValueOrOffset+i*8+8], c.ValueOrOffset[i])
	}
	for i := uint32(0); i < c.N; i++ {
		binary.LittleEndian.PutUint64(buf[offSize+i*8:offSize+i*8+8], c.Size[i])
	}
	return buf, nil
}

// Decode parses a Columns from buf. Unlike a hardcoded per-version layout,
// the column count and each column's kind/width are read from the on-disk
// directory (docs/docs/archive/06-toc-format.md §3-4): a directory entry
// whose kind this build doesn't recognize is skipped using its own declared
// Width, never a hardcoded one — the forward-compatible path for a column
// added by a later minor version. VersionMajor remains the gate for
// incompatible *semantic* changes to the six required columns; it is not
// needed to determine how many columns exist or how wide they are.
func Decode(buf []byte) (*Columns, error) {
	if len(buf) < HeaderSize {
		return nil, fmt.Errorf("toc: buffer too short for header: %d < %d", len(buf), HeaderSize)
	}
	c := &Columns{
		VersionMajor: binary.LittleEndian.Uint16(buf[0:2]),
		VersionMinor: binary.LittleEndian.Uint16(buf[2:4]),
		N:            binary.LittleEndian.Uint32(buf[4:8]),
	}
	columnCount := binary.LittleEndian.Uint16(buf[8:10])

	dirBytes := uint32(columnCount) * dirEntrySize
	if uint32(len(buf)) < uint32(HeaderSize)+dirBytes {
		return nil, fmt.Errorf("toc: buffer too short for column directory: %d < %d", len(buf), uint32(HeaderSize)+dirBytes)
	}
	entries := make([]dirEntry, columnCount)
	for i := range entries {
		base := uint32(HeaderSize) + uint32(i)*dirEntrySize
		entries[i] = dirEntry{
			Kind:  ColumnKind(binary.LittleEndian.Uint16(buf[base : base+2])),
			Width: buf[base+2],
			// buf[base+3] reserved — not enforced, same convention as
			// reserved fields elsewhere in the format (03-storage-format.md
			// prologue).
		}
	}

	offsets, total := columnOffsets(entries, c.N)
	if uint32(len(buf)) < total {
		return nil, fmt.Errorf("toc: buffer too short: %d < %d (N=%d)", len(buf), total, c.N)
	}

	idxType, err := findColumn(entries, ColumnKindType)
	if err != nil {
		return nil, err
	}
	idxRole, err := findColumn(entries, ColumnKindRole)
	if err != nil {
		return nil, err
	}
	idxParent, err := findColumn(entries, ColumnKindParent)
	if err != nil {
		return nil, err
	}
	idxSibling, err := findColumn(entries, ColumnKindSibling)
	if err != nil {
		return nil, err
	}
	idxValueOrOffset, err := findColumn(entries, ColumnKindValueOrOffset)
	if err != nil {
		return nil, err
	}
	idxSize, err := findColumn(entries, ColumnKindSize)
	if err != nil {
		return nil, err
	}

	offType, offRole, offParent := offsets[idxType], offsets[idxRole], offsets[idxParent]
	offSibling, offValueOrOffset, offSize := offsets[idxSibling], offsets[idxValueOrOffset], offsets[idxSize]

	c.Type = make([]mediatree.NodeType, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Type[i] = mediatree.NodeType(buf[offType+i])
	}
	c.Role = make([]mediatree.Role, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Role[i] = mediatree.Role(binary.LittleEndian.Uint16(buf[offRole+i*2 : offRole+i*2+2]))
	}
	c.Parent = make([]uint32, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Parent[i] = binary.LittleEndian.Uint32(buf[offParent+i*4 : offParent+i*4+4])
	}
	c.Sibling = make([]uint32, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Sibling[i] = binary.LittleEndian.Uint32(buf[offSibling+i*4 : offSibling+i*4+4])
	}
	c.ValueOrOffset = make([]uint64, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.ValueOrOffset[i] = binary.LittleEndian.Uint64(buf[offValueOrOffset+i*8 : offValueOrOffset+i*8+8])
	}
	c.Size = make([]uint64, c.N)
	for i := uint32(0); i < c.N; i++ {
		c.Size[i] = binary.LittleEndian.Uint64(buf[offSize+i*8 : offSize+i*8+8])
	}
	return c, nil
}
