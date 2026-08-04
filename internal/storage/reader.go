package storage

import (
	"fmt"
	"sort"

	"traycers/farc/fblock"
	"traycers/farc/toc"
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

func (u *Unit) readEpilogAt(idx uint32) (fblock.Epilog, error) {
	off := int64(fblockOffset(u.geo, idx)) + int64(u.geo.FblockSize) - int64(fblock.EpilogSize)
	buf, err := u.readRange(off, int64(fblock.EpilogSize))
	if err != nil {
		return fblock.Epilog{}, err
	}
	return fblock.DecodeEpilog(buf)
}

// contentBaseOffset returns the absolute offset where fblock idx's Content
// section begins. Each fblock is self-contained (ADR-002) and may have
// been written under a different params_size/catalog_size than others (if
// operator params changed since), so this always reads that fblock's own
// fixed prolog rather than assuming Unit's current geometry-adjacent sizes.
func (u *Unit) contentBaseOffset(idx uint32) (int64, error) {
	buf, err := u.readRange(int64(fblockOffset(u.geo, idx)), int64(fblock.FixedPrologSize))
	if err != nil {
		return 0, err
	}
	prolog, err := fblock.DecodeFixedProlog(buf)
	if err != nil {
		return 0, err
	}
	offs := fblock.ComputeOffsets(prolog.ParamsSize, prolog.CatalogSize)
	return int64(fblockOffset(u.geo, idx)) + int64(offs.ContentOffset), nil
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
	if epilog.TOCSize == 0 {
		return nil, fmt.Errorf("storage: reader: fcontainer %x has an empty TOC", uuid)
	}
	tocStart := int64(fblockOffset(u.geo, idx)) + int64(u.geo.FblockSize) - int64(fblock.TOCOffsetFromEnd(epilog.TOCSize))
	buf, err := u.readRange(tocStart, int64(epilog.TOCSize))
	if err != nil {
		return nil, fmt.Errorf("storage: reader: read TOC for fblock %d: %w", idx, err)
	}
	return toc.Decode(buf)
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
	base, err := u.contentBaseOffset(idx)
	if err != nil {
		return 0, fmt.Errorf("storage: reader: fblock %d content offset: %w", idx, err)
	}
	epilog, err := u.readEpilogAt(idx)
	if err != nil {
		return 0, fmt.Errorf("storage: reader: read epilog for fblock %d: %w", idx, err)
	}
	tocStart := int64(fblockOffset(u.geo, idx)) + int64(u.geo.FblockSize) - int64(fblock.TOCOffsetFromEnd(epilog.TOCSize))
	return tocStart - base, nil
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
		return nil, fmt.Errorf("storage: reader: fblock %d content offset: %w", idx, err)
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
		return nil, fmt.Errorf("storage: reader: fblock %d content offset: %w", idx, err)
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
