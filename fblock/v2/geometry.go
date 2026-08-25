package v2

import (
	"errors"
	"fmt"
)

// ContentSize is the approximate content-node value capacity for a v2.0
// fblock given known params/catalog/toc sizes: fblock_size minus every
// other node's total on-disk footprint (root/params/catalog/toc,
// NodeTotalSize) and the epilog, minus content's own fixed header+trailer
// overhead. It ignores content's own padding (which depends on its value
// size — the very thing being solved for), so it's a conservative
// underestimate by up to alignment-1 bytes — used for fullness pre-checks
// and capacity estimates (mirroring fblock.ContentSize's v1.0 role), not
// as an exact on-disk guarantee: AssembleFblock's own padToSize
// bounds-check is authoritative there.
func ContentSize(fblockSize uint64, paramsSize, catalogSize, tocSize uint32, alignment int) int64 {
	overhead := NodeTotalSize(0, alignment) + // root
		NodeTotalSize(int64(paramsSize), alignment) +
		NodeTotalSize(int64(catalogSize), alignment) +
		NodeTotalSize(int64(tocSize), alignment) +
		int64(fixedHeaderSize+trailerSize) + // content's own fixed overhead
		int64(EpilogSizeV2)
	return int64(fblockSize) - overhead
}

// MaxContainerSize returns the maximum combined content+TOC capacity
// (ADR-013) for a fblock of the given size with the given params/catalog
// sizes — defined as ContentSize with tocSize=0, exactly like v1.0's
// fblock.MaxContainerSize/fblock.ContentSize relationship.
func MaxContainerSize(fblockSize uint64, paramsSize, catalogSize uint32, alignment int) int64 {
	return ContentSize(fblockSize, paramsSize, catalogSize, 0, alignment)
}

// ErrContainerShareTooSmall is returned when the computed max fcontainer
// size falls below min_container_share * fblock_size (ADR-013) — the
// v2.0 analog of fblock.ErrContainerShareTooSmall.
var ErrContainerShareTooSmall = errors.New("fblock/v2: max fcontainer size below min_container_share")

// CheckMinContainerShare validates the ADR-013 geometry invariant for
// format v2.0, run at Storage init and at every expand/shrink — the v2.0
// analog of fblock.CheckMinContainerShare.
func CheckMinContainerShare(fblockSize uint64, paramsSize, catalogSize uint32, minShare float64, alignment int) error {
	maxSize := MaxContainerSize(fblockSize, paramsSize, catalogSize, alignment)
	minSize := minShare * float64(fblockSize)
	if float64(maxSize) < minSize {
		return fmt.Errorf("%w: max=%d bytes, required>=%.0f bytes (share=%v, fblock_size=%d)",
			ErrContainerShareTooSmall, maxSize, minSize, minShare, fblockSize)
	}
	return nil
}
