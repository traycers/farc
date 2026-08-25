package storage

import (
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/ioengine"
)

// readFblockV2 reads and decodes a complete v2.0 fblock at idx (fixed
// prolog, the tree of root/params/catalog/content/toc nodes, and the
// epilog directory) — additive alongside the v1.0 path (headerio.go).
// Nothing in internal/storage's real read/write path calls this yet.
func readFblockV2(backend ioengine.Backend, geo Geometry, idx uint32) (fblockv2.Tree, error) {
	buf := make([]byte, geo.FblockSize)
	_, err := backend.ReadAt(buf, int64(fblockOffset(geo, idx)))
	if err != nil {
		return fblockv2.Tree{}, err
	}
	return fblockv2.ReadFblock(buf, backend.Alignment())
}

// writeFblockV2 assembles a complete v2.0 fblock (§12.1) and writes it at
// idx's offset in one shot — a test/PoC helper for the v2.0 codec, not the
// progressive per-node write path the real StorageEngine will need
// (ADR-023 §"Решение").
func writeFblockV2(backend ioengine.Backend, geo Geometry, idx uint32, prolog fblockv2.FixedProlog, params, catalog, content, toc []byte) error {
	buf, err := fblockv2.AssembleFblock(prolog, params, catalog, content, toc, backend.Alignment(), geo.FblockSize)
	if err != nil {
		return err
	}
	_, err = backend.WriteAt(buf, int64(fblockOffset(geo, idx)))
	return err
}
