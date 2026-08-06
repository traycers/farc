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
// with the given params and catalog sizes.
func ComputeOffsets(paramsSize, catalogSize uint32) Offsets {
	var o Offsets
	o.ParamsOffset = FixedPrologSize
	o.MagicCatalogOffset = o.ParamsOffset + uint64(paramsSize)
	o.CatalogOffset = o.MagicCatalogOffset + 8
	o.ChecksumsOffset = o.CatalogOffset + uint64(catalogSize)
	o.MagicContentOffset = o.ChecksumsOffset + HeaderChecksumsSize
	o.ContentOffset = o.MagicContentOffset + 8
	return o
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
// fixed geometry: fblock_size - 112 - params_size - catalog_size - toc_size.
func ContentSize(fblockSize uint64, paramsSize, catalogSize, tocSize uint32) int64 {
	return int64(fblockSize) - FixedOverheadBytes - int64(paramsSize) - int64(catalogSize) - int64(tocSize)
}

// MaxContainerSize returns the maximum fcontainer size (content+TOC
// combined) for a fblock of the given size with the given params/catalog
// sizes (docs/docs/archive/03-storage-format.md §8.3).
func MaxContainerSize(fblockSize uint64, paramsSize, catalogSize uint32) int64 {
	return int64(fblockSize) - FixedOverheadBytes - int64(paramsSize) - int64(catalogSize)
}

// ErrContainerShareTooSmall is returned when the computed max fcontainer
// size falls below min_container_share * fblock_size (ADR-013).
var ErrContainerShareTooSmall = errors.New("fblock: max fcontainer size below min_container_share")

// CheckMinContainerShare validates the ADR-013 geometry invariant, run at
// Storage init and at every expand/shrink (docs/docs/archive/
// 03-storage-format.md §8.3).
func CheckMinContainerShare(fblockSize uint64, paramsSize, catalogSize uint32, minShare float64) error {
	maxSize := MaxContainerSize(fblockSize, paramsSize, catalogSize)
	minSize := minShare * float64(fblockSize)
	if float64(maxSize) < minSize {
		return fmt.Errorf("%w: max=%d bytes, required>=%.0f bytes (share=%v, fblock_size=%d)",
			ErrContainerShareTooSmall, maxSize, minSize, minShare, fblockSize)
	}
	return nil
}
