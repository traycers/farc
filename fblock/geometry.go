package fblock

import (
	"errors"
	"fmt"
)

// FixedOverheadBytes is the total fixed-size overhead per fblock outside of
// params/catalog/TOC: 56 (fixed prolog) + 8 (magic_catalog) + 12 (header
// checksums) + 8 (magic_content) + 8 (magic_toc) + 20 (epilog) = 112 bytes
// (docs/docs/archive/03-storage-format.md §8.3, §10).
const FixedOverheadBytes = 112

// HeaderChecksumsSize is the size of the three CRC32 header checksums
// (docs/docs/archive/03-storage-format.md §7).
const HeaderChecksumsSize = 12

// Offsets holds the byte offsets of each fblock section, measured from the
// start of the fblock, for a given params/catalog size (docs/docs/archive/
// 03-storage-format.md §10 "Карта смещений"). TOC/epilog offsets are
// measured from the end instead (see MagicTOCOffsetFromEnd, TOCOffsetFromEnd,
// EpilogSize) since they depend on toc_size, known only from the epilog.
type Offsets struct {
	ParamsOffset       uint64
	MagicCatalogOffset uint64
	CatalogOffset      uint64
	ChecksumsOffset    uint64
	MagicContentOffset uint64
	ContentOffset      uint64
}

// ComputeOffsets computes the start-relative section offsets for a fblock
// with the given params and catalog sizes. alignment is the backing
// backend's required I/O alignment (ioengine.Backend.Alignment(), 1 for a
// backend with no alignment requirement) — ContentOffset is rounded up to
// it, reserving the same zero-byte gap between magic_content and the first
// real content byte that storageengine.EnqueueOpenWrite physically writes
// as headerPadLen for a periodic-flush job, so every reader locates content
// exactly where a writer (either write path — see contentGap) actually put
// it (.scratch/fblocks-ui/issues/10-header-pad-content-offset-mismatch.md).
// A caller that only needs ChecksumsOffset (never ContentOffset) may pass
// any alignment, including 1 — it doesn't affect anything before
// MagicContentOffset.
func ComputeOffsets(paramsSize, catalogSize uint32, alignment int) Offsets {
	var o Offsets
	o.ParamsOffset = FixedPrologSize
	o.MagicCatalogOffset = o.ParamsOffset + uint64(paramsSize)
	o.CatalogOffset = o.MagicCatalogOffset + 8
	o.ChecksumsOffset = o.CatalogOffset + uint64(catalogSize)
	o.MagicContentOffset = o.ChecksumsOffset + HeaderChecksumsSize
	contentStart := o.MagicContentOffset + 8
	o.ContentOffset = roundUp(contentStart, alignment)
	return o
}

// roundUp rounds n up to the nearest multiple of alignment. alignment <= 1
// means "no alignment requirement" and is a no-op.
func roundUp(n uint64, alignment int) uint64 {
	if alignment <= 1 {
		return n
	}
	a := uint64(alignment)
	if rem := n % a; rem != 0 {
		return n + (a - rem)
	}
	return n
}

// contentGap returns how many extra bytes ComputeOffsets's ContentOffset
// reserves beyond the unaligned content start, for the given
// params/catalog sizes and alignment — the same quantity ContentSize/
// MaxContainerSize must subtract from the content budget so a fully
// assembled fblock (header + gap + content + toc + epilog) still sums to
// exactly fblock_size.
func contentGap(paramsSize, catalogSize uint32, alignment int) int64 {
	offs := ComputeOffsets(paramsSize, catalogSize, alignment)
	unaligned := offs.MagicContentOffset + 8
	return int64(offs.ContentOffset - unaligned)
}

// MagicTOCOffsetFromEnd returns the magic_toc offset, counted backward from
// the end of the fblock.
func MagicTOCOffsetFromEnd(tocSize uint32) uint64 {
	return uint64(EpilogSize) + uint64(tocSize) + 8
}

// TOCOffsetFromEnd returns the TOC section's start offset, counted backward
// from the end of the fblock.
func TOCOffsetFromEnd(tocSize uint32) uint64 {
	return uint64(EpilogSize) + uint64(tocSize)
}

// ContentSize computes the actual content size implied by the surrounding
// fixed geometry: fblock_size - 112 - params_size - catalog_size - toc_size
// - contentGap(alignment). alignment is the backing backend's required I/O
// alignment, exactly as passed to ComputeOffsets — see that function's doc
// comment.
func ContentSize(fblockSize uint64, paramsSize, catalogSize, tocSize uint32, alignment int) int64 {
	return int64(fblockSize) - FixedOverheadBytes - int64(paramsSize) - int64(catalogSize) - int64(tocSize) - contentGap(paramsSize, catalogSize, alignment)
}

// MaxContainerSize returns the maximum fcontainer size (content+TOC
// combined) for a fblock of the given size with the given params/catalog
// sizes (docs/docs/archive/03-storage-format.md §8.3). alignment is as in
// ComputeOffsets.
func MaxContainerSize(fblockSize uint64, paramsSize, catalogSize uint32, alignment int) int64 {
	return int64(fblockSize) - FixedOverheadBytes - int64(paramsSize) - int64(catalogSize) - contentGap(paramsSize, catalogSize, alignment)
}

// ErrContainerShareTooSmall is returned when the computed max fcontainer
// size falls below min_container_share * fblock_size (ADR-013).
var ErrContainerShareTooSmall = errors.New("fblock: max fcontainer size below min_container_share")

// CheckMinContainerShare validates the ADR-013 geometry invariant, run at
// Storage init and at every expand/shrink (docs/docs/archive/
// 03-storage-format.md §8.3). alignment is as in ComputeOffsets.
func CheckMinContainerShare(fblockSize uint64, paramsSize, catalogSize uint32, minShare float64, alignment int) error {
	maxSize := MaxContainerSize(fblockSize, paramsSize, catalogSize, alignment)
	minSize := minShare * float64(fblockSize)
	if float64(maxSize) < minSize {
		return fmt.Errorf("%w: max=%d bytes, required>=%.0f bytes (share=%v, fblock_size=%d)",
			ErrContainerShareTooSmall, maxSize, minSize, minShare, fblockSize)
	}
	return nil
}
