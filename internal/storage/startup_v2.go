package storage

import (
	"fmt"
	"sort"

	"github.com/traycers/farc/fblock"
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/ioengine"
)

// probeGeometryV2 reads fblock 0's v2.0 fixed prolog to learn Storage's
// fixed geometry — the v2.0 analog of probeGeometry (startup.go).
func probeGeometryV2(backend ioengine.Backend) (Geometry, error) {
	buf := make([]byte, fblockv2.FixedPrologSizeV2)
	_, err := backend.ReadAt(buf, 0)
	if err != nil {
		return Geometry{}, fmt.Errorf("storage: probe geometry (v2): %w", err)
	}
	prolog, err := fblockv2.DecodeFixedProlog(buf)
	if err != nil {
		return Geometry{}, fmt.Errorf("%w: fblock 0 unreadable: %w", ErrStorageCorrupted, err)
	}
	return Geometry{
		FblockSize:  prolog.FblockSize,
		N:           prolog.CatalogEntryCount,
		MaxChannels: prolog.MaxChannels,
	}, nil
}

// scanForFreshestCatalogV2 is the v2.0 analog of scanForFreshestCatalog:
// read every fblock's fixed prolog, rank candidates by write_sequence
// descending, and return the first (highest-write_sequence) one whose
// catalog node — located directly via its epilog row's offset/size, no
// geometry arithmetic needed — actually decodes. A candidate whose epilog
// shows Count<3 (catalog not even confirmed yet) is skipped, same as
// v1.0 skipping a candidate whose header diagnosis isn't CatalogValid.
func scanForFreshestCatalogV2(backend ioengine.Backend, geo Geometry) (*fblock.Catalog, uint32, error) {
	type candidate struct {
		idx uint32
		seq uint64
	}
	var candidates []candidate
	for i := uint32(0); i < geo.N; i++ {
		buf := make([]byte, fblockv2.FixedPrologSizeV2)
		_, err := backend.ReadAt(buf, int64(fblockOffset(geo, i)))
		if err != nil {
			continue
		}
		prolog, err := fblockv2.DecodeFixedProlog(buf)
		if err != nil {
			continue // ErrUninitialized or a bad read: not a candidate
		}
		candidates = append(candidates, candidate{i, prolog.WriteSequence})
	}
	if len(candidates) == 0 {
		return nil, 0, fmt.Errorf("%w: no fblock with a valid magic_prolog found", ErrStorageCorrupted)
	}
	sort.Slice(candidates, func(a, b int) bool { return candidates[a].seq > candidates[b].seq })

	for _, c := range candidates {
		base := int64(fblockOffset(geo, c.idx))

		prologBuf := make([]byte, fblockv2.FixedPrologSizeV2)
		_, err := backend.ReadAt(prologBuf, base)
		if err != nil {
			continue
		}
		prolog, err := fblockv2.DecodeFixedProlog(prologBuf)
		if err != nil {
			continue
		}

		epilogBuf := make([]byte, fblockv2.EpilogSizeV2)
		epilogOff := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
		_, err = backend.ReadAt(epilogBuf, epilogOff)
		if err != nil {
			continue
		}
		epilog, err := fblockv2.DecodeEpilog(epilogBuf)
		if err != nil || epilog.Count < 3 {
			continue
		}

		catalogRow := epilog.Rows[2]
		nodeBuf := make([]byte, catalogRow.Size)
		_, err = backend.ReadAt(nodeBuf, base+int64(catalogRow.Offset))
		if err != nil {
			continue
		}
		node, _, err := fblockv2.DecodeNode(nodeBuf)
		if err != nil {
			continue
		}
		cat, err := fblock.DecodeCatalog(node.Value, prolog.MaxChannels, prolog.CatalogEntryCount)
		if err != nil {
			continue
		}
		return cat, c.idx, nil
	}
	return nil, 0, fmt.Errorf("%w: every candidate fblock's catalog snapshot is corrupted", ErrStorageCorrupted)
}

// readParamsAndPrologV2 reads fblock idx's fixed prolog and decodes its
// params node — the v2.0 analog of reading a v1.0 header's Prolog/Params
// fields together (Open's "current operative params come from the cursor
// fblock" step). Requires the params row (epilog index 1) to be present,
// i.e. Count>=2 — true by construction for any idx scanForFreshestCatalogV2
// already accepted (it requires Count>=3).
func readParamsAndPrologV2(backend ioengine.Backend, geo Geometry, idx uint32) (fblockv2.FixedProlog, fblock.Params, error) {
	base := int64(fblockOffset(geo, idx))

	prologBuf := make([]byte, fblockv2.FixedPrologSizeV2)
	_, err := backend.ReadAt(prologBuf, base)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: read prolog for fblock %d: %w", idx, err)
	}
	prolog, err := fblockv2.DecodeFixedProlog(prologBuf)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: decode prolog for fblock %d: %w", idx, err)
	}

	epilogBuf := make([]byte, fblockv2.EpilogSizeV2)
	epilogOff := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)
	_, err = backend.ReadAt(epilogBuf, epilogOff)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: read epilog for fblock %d: %w", idx, err)
	}
	epilog, err := fblockv2.DecodeEpilog(epilogBuf)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: decode epilog for fblock %d: %w", idx, err)
	}
	if epilog.Count < 2 {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("%w: fblock %d params not confirmed (epilog count=%d)", ErrStorageCorrupted, idx, epilog.Count)
	}

	paramsRow := epilog.Rows[1]
	nodeBuf := make([]byte, paramsRow.Size)
	_, err = backend.ReadAt(nodeBuf, base+int64(paramsRow.Offset))
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: read params node for fblock %d: %w", idx, err)
	}
	node, _, err := fblockv2.DecodeNode(nodeBuf)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: decode params node for fblock %d: %w", idx, err)
	}
	params, err := fblock.DecodeParams(node.Value)
	if err != nil {
		return fblockv2.FixedProlog{}, fblock.Params{}, fmt.Errorf("storage: decode params for fblock %d: %w", idx, err)
	}
	return prolog, params, nil
}
