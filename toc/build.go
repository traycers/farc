package toc

import (
	"errors"

	"github.com/traycers/farc/mediatree"
)

// Build constructs the TOC for a fcontainer's Content, following the DFS
// reorder algorithm in docs/docs/archive/06-toc-format.md §5 ("Method B"
// from docs/docs/archive/08-array-trees.md §8.2): depth -> bottom-up subtree
// size -> top-down DFS position via prefix sum over sibling chains ->
// permute+translate. elems and valueOffsets must be in Content creation
// order (id = index), e.g. from mediatree.DecodeContentWithOffsets.
//
// Content itself is never touched — only the derived TOC is permuted
// (docs/docs/archive/08-array-trees.md §8.4).
func Build(elems []mediatree.Element, valueOffsets []uint64) (*Columns, error) {
	n := uint32(len(elems))
	if n == 0 {
		return nil, errEmptyTree
	}
	if len(valueOffsets) != len(elems) {
		return nil, errOffsetsLenMismatch
	}

	// Step 2 (depth is not needed by Method B itself beyond ordering, which
	// the parent[i]<=id invariant already gives us for free) is skipped —
	// unlike CoveringSubtreeRoot's LCA-of-set query, Build's own steps 3-4
	// (subtree size, DFS position) only need the parent<=id invariant, not
	// depth values themselves. This matches 08-array-trees.md §8.2 step 1
	// listing depth as a prerequisite for its *general* bottom-up-by-level
	// framing; the id-descending single pass below achieves the same
	// bottom-up order directly from parent[i]<=id, without materializing
	// depth as an intermediate.

	// Step 3 (doc numbering): subtree size, bottom-up. Processing ids in
	// decreasing order guarantees every child of i has already contributed
	// to size[i] before size[i] itself is added to size[parent[i]], because
	// parent[i]<=i means any child j of i satisfies j>i... actually the
	// needed fact is simpler: any node j with parent[j]==i has j>i is NOT
	// guaranteed in general array-based trees, but IS guaranteed here since
	// parent[j]<=j always, and a child is created after its parent, i.e.
	// j>i strictly for i's children (i itself can't be its own child) — so
	// descending order visits every child before its parent.
	size := make([]uint32, n)
	for i := range size {
		size[i] = 1
	}
	for i := int64(n) - 1; i >= 0; i-- {
		id := uint32(i)
		if elems[id].Parent != id {
			size[elems[id].Parent] += size[id]
		}
	}

	// Step 4: DFS position, top-down. Processing ids in increasing order is
	// valid because parent[i]<=i and sibling[i]<=i, so both dependencies of
	// pos[i] (pos[parent[i]] and leftSum[sibling[i]]) are already computed.
	pos := make([]uint32, n) // old id -> new position (this IS old2new)
	leftSum := make([]uint32, n)
	for i := uint32(0); i < n; i++ {
		if elems[i].Parent == i {
			pos[i] = 0
			continue
		}
		if elems[i].Sibling != i {
			leftSum[i] = leftSum[elems[i].Sibling] + size[elems[i].Sibling]
		}
		pos[i] = pos[elems[i].Parent] + 1 + leftSum[i]
	}

	new2old := make([]uint32, n)
	for oldID := uint32(0); oldID < n; oldID++ {
		new2old[pos[oldID]] = oldID
	}

	c := &Columns{
		VersionMajor:  1,
		VersionMinor:  0,
		N:             n,
		Type:          make([]mediatree.NodeType, n),
		Role:          make([]mediatree.Role, n),
		Parent:        make([]uint32, n),
		Sibling:       make([]uint32, n),
		ValueOrOffset: make([]uint64, n),
		Size:          make([]uint64, n),
	}

	for newID := uint32(0); newID < n; newID++ {
		oldID := new2old[newID]
		e := elems[oldID]
		c.Type[newID] = e.Type
		c.Role[newID] = e.Role
		c.Parent[newID] = pos[e.Parent] // translate: old2new(oldParent)
		c.Sibling[newID] = pos[e.Sibling]

		if fixedSize, ok := e.Type.FixedSize(); ok {
			c.ValueOrOffset[newID] = packInline(e.Value, fixedSize)
			c.Size[newID] = 0
		} else {
			c.ValueOrOffset[newID] = valueOffsets[oldID]
			c.Size[newID] = uint64(len(e.Value))
		}
	}

	return c, nil
}

// packInline packs a fixed-width value's bytes into the low-order bytes of
// a little-endian uint64 (docs/docs/archive/06-toc-format.md §4.1). void has
// fixedSize 0, producing 0.
func packInline(value []byte, fixedSize int) uint64 {
	var v uint64
	for i := 0; i < fixedSize && i < len(value) && i < 8; i++ {
		v |= uint64(value[i]) << (8 * uint(i))
	}
	return v
}

// unpackInline is the inverse of packInline, returning the value's raw bytes.
func unpackInline(v uint64, fixedSize int) []byte {
	if fixedSize == 0 {
		return nil
	}
	buf := make([]byte, fixedSize)
	for i := 0; i < fixedSize && i < 8; i++ {
		buf[i] = byte(v >> (8 * uint(i)))
	}
	return buf
}

var (
	errEmptyTree          = errors.New("toc: cannot build TOC for an empty tree")
	errOffsetsLenMismatch = errors.New("toc: valueOffsets length must match elems length")
)
