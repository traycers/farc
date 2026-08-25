package storage

import (
	"fmt"
	"sort"

	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/toc"
)

// Range is one (offset, size) request into a fcontainer's Content section,
// offset relative to Content's own start (as toc.ContentOffset returns).
type Range struct {
	Offset uint64
	Size   uint64
}

// ResolveUUID returns the physical index of the Ready fblock holding the
// fcontainer with this UUID, if any (docs/docs/archive/
// 04-storage-operations.md §8.2).
func (u *Unit) ResolveUUID(uuid [16]byte) (uint32, bool) {
	return u.mgr.ResolveUUID(uuid)
}

// Candidates narrows to Ready fblocks whose [begin,end] overlaps [t1,t2]
// and which carry channelNumber (docs/docs/archive/04-storage-operations.md
// §8.1, ADR-014) — exact confirmation still requires reading each
// candidate's TOC.
func (u *Unit) Candidates(channelNumber uint16, t1, t2 uint64) []uint32 {
	return u.mgr.Candidates(channelNumber, t1, t2)
}

// LatestReadyForChannel returns the physical index of channelNumber's most
// recently completed (Ready, largest End) fblock -- the fblock-live WS
// handler's (internal/api) "previous fblock" link.
func (u *Unit) LatestReadyForChannel(channelNumber uint16) (uint32, bool) {
	return u.mgr.LatestReadyForChannel(channelNumber)
}

// readRange performs one arbitrated read through the StorageEngine
// (ADR-005/ADR-011) — the only way Reader ever touches disk, so a
// concurrent Recorder write is never bypassed or interrupted incorrectly.
func (u *Unit) readRange(offset, length int64) ([]byte, error) {
	ticket := u.engine.EnqueueRead(offset, length)
	buf, err := ticket.Wait()
	if err != nil {
		return nil, err
	}
	u.health.RecordRead()
	return buf, nil
}

// readEpilogAt reads and decodes fblock idx's v2.0 epilog — fixed size,
// fixed offset from the end, found without scanning (ADR-023 §12.5).
func (u *Unit) readEpilogAt(idx uint32) (fblockv2.Epilog, error) {
	off := int64(fblockOffset(u.geo, idx)) + int64(u.geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	buf, err := u.readRange(off, int64(fblockv2.EpilogSizeV2))
	if err != nil {
		return fblockv2.Epilog{}, err
	}
	return fblockv2.DecodeEpilog(buf)
}

// contentNodeOffsetAndSize returns fblock idx's content node's absolute
// start offset and total on-disk size (header+value+padding+trailer),
// read directly from its epilog row — no geometry arithmetic needed
// (ADR-023 §"Решение": this is exactly what the epilog directory is for).
func (u *Unit) contentNodeOffsetAndSize(idx uint32) (int64, uint64, error) {
	epilog, err := u.readEpilogAt(idx)
	if err != nil {
		return 0, 0, err
	}
	row := epilog.Rows[fblockv2.NodeTypeContent]
	return int64(fblockOffset(u.geo, idx)) + int64(row.Offset), row.Size, nil
}

// contentBaseOffset returns the absolute offset where fblock idx's content
// *value* begins (skipping the content node's own fixed header).
func (u *Unit) contentBaseOffset(idx uint32) (int64, error) {
	nodeOffset, _, err := u.contentNodeOffsetAndSize(idx)
	if err != nil {
		return 0, fmt.Errorf("storage: reader: fblock %d content offset: %w", idx, err)
	}
	return nodeOffset + int64(fblockv2.FixedHeaderSize), nil
}

// ReadTOC resolves uuid to its Ready fblock and reads/decodes its TOC
// section (docs/docs/archive/04-storage-operations.md §8.3).
func (u *Unit) ReadTOC(uuid [16]byte) (*toc.Columns, error) {
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		return nil, fmt.Errorf("storage: reader: fcontainer %x not found (not Ready)", uuid)
	}
	epilog, err := u.readEpilogAt(idx)
	if err != nil {
		return nil, fmt.Errorf("storage: reader: read epilog for fblock %d: %w", idx, err)
	}
	tocRow := epilog.Rows[fblockv2.NodeTypeTOC]
	if tocRow.Size == 0 {
		return nil, fmt.Errorf("storage: reader: fcontainer %x has an empty TOC", uuid)
	}
	nodeBuf, err := u.readRange(int64(fblockOffset(u.geo, idx))+int64(tocRow.Offset), int64(tocRow.Size))
	if err != nil {
		return nil, fmt.Errorf("storage: reader: read TOC for fblock %d: %w", idx, err)
	}
	node, _, err := fblockv2.DecodeNode(nodeBuf)
	if err != nil {
		return nil, fmt.Errorf("storage: reader: decode TOC node for fblock %d: %w", idx, err)
	}
	return toc.Decode(node.Value)
}

// ContentSize returns the total size of uuid's Content section — for a
// whole-fcontainer export (no ranges given), the caller needs this to know
// how much to read, since (unlike a single ranged read) there's no
// caller-supplied size to use directly.
func (u *Unit) ContentSize(uuid [16]byte) (int64, error) {
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		return 0, fmt.Errorf("storage: reader: fcontainer %x not found (not Ready)", uuid)
	}
	epilog, err := u.readEpilogAt(idx)
	if err != nil {
		return 0, fmt.Errorf("storage: reader: read epilog for fblock %d: %w", idx, err)
	}
	contentRow := epilog.Rows[fblockv2.NodeTypeContent]
	// The row's Size is the node's total on-disk footprint (header+value+
	// padding+trailer); the real value length is what ReadTOC/ReadRange
	// callers actually want, so decode the node's header to get value_size
	// rather than assuming the two are equal.
	nodeBuf, err := u.readRange(int64(fblockOffset(u.geo, idx))+int64(contentRow.Offset), int64(contentRow.Size))
	if err != nil {
		return 0, fmt.Errorf("storage: reader: read content node for fblock %d: %w", idx, err)
	}
	node, _, err := fblockv2.DecodeNode(nodeBuf)
	if err != nil {
		return 0, fmt.Errorf("storage: reader: decode content node for fblock %d: %w", idx, err)
	}
	return int64(len(node.Value)), nil
}

// ReadRange reads size bytes at offset within uuid's Content section
// (docs/docs/archive/04-storage-operations.md §8.4, single-range case).
func (u *Unit) ReadRange(uuid [16]byte, offset, size uint64) ([]byte, error) {
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		return nil, fmt.Errorf("storage: reader: fcontainer %x not found (not Ready)", uuid)
	}
	base, err := u.contentBaseOffset(idx)
	if err != nil {
		return nil, err
	}
	buf, err := u.readRange(base+int64(offset), int64(size))
	if err != nil {
		return nil, fmt.Errorf("storage: reader: read range [%d,%d) of fblock %d: %w", offset, offset+size, idx, err)
	}
	return buf, nil
}

// ReadRanges reads every requested range from uuid's Content section,
// issuing the underlying reads in offset order for seek locality
// (docs/docs/archive/04-storage-operations.md §8.4: "группирует и
// сортирует диапазоны"), but returns results in the caller's original
// order.
func (u *Unit) ReadRanges(uuid [16]byte, ranges []Range) ([][]byte, error) {
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		return nil, fmt.Errorf("storage: reader: fcontainer %x not found (not Ready)", uuid)
	}
	base, err := u.contentBaseOffset(idx)
	if err != nil {
		return nil, err
	}

	order := make([]int, len(ranges))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return ranges[order[a]].Offset < ranges[order[b]].Offset })

	out := make([][]byte, len(ranges))
	for _, i := range order {
		r := ranges[i]
		buf, err := u.readRange(base+int64(r.Offset), int64(r.Size))
		if err != nil {
			return nil, fmt.Errorf("storage: reader: range %d [%d,%d) of fblock %d: %w", i, r.Offset, r.Offset+r.Size, idx, err)
		}
		out[i] = buf
	}
	return out, nil
}

// ReadNodeValue returns node nodeID's value bytes: inline directly from
// columns for fixed-width types, or read from uuid's Content section (via
// its recorded offset/size) for variable-width ones.
func (u *Unit) ReadNodeValue(uuid [16]byte, columns *toc.Columns, nodeID uint32) ([]byte, error) {
	if v, ok := toc.InlineValue(columns, nodeID); ok {
		return v, nil
	}
	offset, size, ok := toc.ContentOffset(columns, nodeID)
	if !ok {
		return nil, fmt.Errorf("storage: reader: node %d has neither an inline value nor a content offset", nodeID)
	}
	return u.ReadRange(uuid, offset, size)
}
