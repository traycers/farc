package storage

import (
	"traycers/farc/fblock"
	"traycers/farc/internal/ioengine"
)

// fblockOffset is offset(index) = index × fblock_size (ADR-001).
func fblockOffset(geo Geometry, idx uint32) uint64 {
	return uint64(idx) * geo.FblockSize
}

// readFixedProlog reads and decodes just the 56-byte fixed prolog of fblock
// idx — enough to learn its write_sequence/params_size/catalog_size without
// paying for a full header read.
func readFixedProlog(backend ioengine.Backend, geo Geometry, idx uint32) (fblock.FixedProlog, error) {
	buf := make([]byte, fblock.FixedPrologSize)
	_, err := backend.ReadAt(buf, int64(fblockOffset(geo, idx)))
	if err != nil {
		return fblock.FixedProlog{}, err
	}
	return fblock.DecodeFixedProlog(buf)
}

// readHeader reads and decodes fblock idx's full header (fixed prolog +
// params + catalog + checksums), sizing the second read from the first.
func readHeader(backend ioengine.Backend, geo Geometry, idx uint32) (fblock.Header, fblock.HeaderDiagnosis, error) {
	prolog, err := readFixedProlog(backend, geo, idx)
	if err != nil {
		return fblock.Header{}, fblock.HeaderDiagnosis{}, err
	}
	offs := fblock.ComputeOffsets(prolog.ParamsSize, prolog.CatalogSize)
	total := offs.ChecksumsOffset + uint64(fblock.HeaderChecksumsSize)
	buf := make([]byte, total)
	_, err = backend.ReadAt(buf, int64(fblockOffset(geo, idx)))
	if err != nil {
		return fblock.Header{}, fblock.HeaderDiagnosis{}, err
	}
	return fblock.DecodeHeader(buf)
}

// readEpilog reads and decodes fblock idx's fixed 20-byte epilog, the last
// bytes of its nominal fblock_size-sized slot.
func readEpilog(backend ioengine.Backend, geo Geometry, idx uint32) (fblock.Epilog, error) {
	buf := make([]byte, fblock.EpilogSize)
	off := int64(fblockOffset(geo, idx)) + int64(geo.FblockSize) - int64(fblock.EpilogSize)
	_, err := backend.ReadAt(buf, off)
	if err != nil {
		return fblock.Epilog{}, err
	}
	return fblock.DecodeEpilog(buf)
}
