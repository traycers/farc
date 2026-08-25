package storage

// fblockOffset is offset(index) = index × fblock_size (ADR-001).
func fblockOffset(geo Geometry, idx uint32) uint64 {
	return uint64(idx) * geo.FblockSize
}
