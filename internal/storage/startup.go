package storage

import (
	"errors"
	"fmt"
	"sort"

	"traycers/farc/fblock"
	"traycers/farc/internal/index"
	"traycers/farc/internal/ioengine"
)

// ErrStorageCorrupted means every candidate fblock failed validation —
// docs/docs/archive/04-storage-operations.md §4.2.1/§4.2.3's "Storage
// считается полностью повреждённым, требуется восстановление через
// CLI-утилиту". Recovering from this is out of v1 scope.
var ErrStorageCorrupted = errors.New("storage: deeply corrupted, needs recovery CLI")

// probeGeometry reads fblock 0's fixed prolog to learn the Storage's fixed
// geometry (docs/docs/archive/04-storage-operations.md §4.2.1 step 1).
// Geometry fields are identical across every fblock by construction, so
// fblock 0 — always present after Init — is as good a source as any.
func probeGeometry(backend ioengine.Backend) (Geometry, error) {
	buf := make([]byte, fblock.FixedPrologSize)
	_, err := backend.ReadAt(buf, 0)
	if err != nil {
		return Geometry{}, fmt.Errorf("storage: probe geometry: %w", err)
	}
	prolog, err := fblock.DecodeFixedProlog(buf)
	if err != nil {
		return Geometry{}, fmt.Errorf("%w: fblock 0 unreadable: %w", ErrStorageCorrupted, err)
	}
	return Geometry{
		FblockSize:  prolog.FblockSize,
		N:           prolog.CatalogEntryCount,
		MaxChannels: prolog.MaxChannels,
	}, nil
}

// scanForFreshestCatalog is Startup path 2 phases 2-3 (docs/docs/archive/
// 04-storage-operations.md §4.2.2-§4.2.3): read every fblock's fixed prolog,
// rank candidates by write_sequence descending, and return the first
// (highest-write_sequence) one whose full header — importantly, its
// catalog CRC — actually validates.
func scanForFreshestCatalog(backend ioengine.Backend, geo Geometry) (*fblock.Catalog, uint32, error) {
	type candidate struct {
		idx uint32
		seq uint64
	}
	var candidates []candidate
	for i := uint32(0); i < geo.N; i++ {
		prolog, err := readFixedProlog(backend, geo, i)
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
		h, diag, err := readHeader(backend, geo, c.idx)
		if err != nil {
			continue
		}
		if diag.CatalogValid {
			return h.Catalog, c.idx, nil
		}
	}
	return nil, 0, fmt.Errorf("%w: every candidate fblock's catalog snapshot is corrupted", ErrStorageCorrupted)
}

// OpenConfig configures Startup (docs/docs/archive/04-storage-operations.md
// §4).
type OpenConfig struct {
	Backend     ioengine.Backend
	CatalogPath string // optional SSD catalog mirror (ADR-007)
	Tuning      EngineTuning
}

// Open runs Startup end to end: path 1 (SSD catalog) with a fallback to
// path 2 (header scan), ConsistencyCheck, and — if path 2 was used, or the
// SSD catalog otherwise needs resyncing after ConsistencyCheck changed
// anything — rewriting the SSD catalog mirror (§4.3). It does not start the
// storageengine.Engine's Run loop; callers drive that themselves (Unit's
// constructor does, via Recorder/Reader).
func Open(cfg OpenConfig) (*Unit, error) {
	geo, err := probeGeometry(cfg.Backend)
	if err != nil {
		return nil, err
	}

	var cat *fblock.Catalog
	var cursor uint32
	usedPath1 := false

	if cfg.CatalogPath != "" {
		c, meta, err := LoadSSDCatalog(cfg.CatalogPath, geo.MaxChannels, geo.N)
		if err == nil {
			cat, cursor, usedPath1 = c, meta.Cursor, true
		}
	}
	if !usedPath1 {
		cat, cursor, err = scanForFreshestCatalog(cfg.Backend, geo)
		if err != nil {
			return nil, err
		}
	}

	// Current operative Params come from the cursor fblock's own header —
	// the freshest write's params are the Storage's current ones (operator
	// changes to write_mode/retention.days only take effect in the next
	// write onward, matching every other per-fblock field).
	hCursor, diag, err := readHeader(cfg.Backend, geo, cursor)
	if err != nil {
		return nil, fmt.Errorf("storage: open: read cursor fblock %d header: %w", cursor, err)
	}
	if diag.Status() != fblock.HeaderIntact {
		return nil, fmt.Errorf("%w: cursor fblock %d header is not intact (%v)", ErrStorageCorrupted, cursor, diag.Status())
	}
	params := hCursor.Params

	mgr := index.New(cat, cursor, params.WriteMode, params.Retention.Days)

	err = ConsistencyCheck(cfg.Backend, geo, mgr)
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}

	if cfg.CatalogPath != "" {
		err := syncSSDCatalog(cfg.CatalogPath, mgr, hCursor.Prolog)
		if err != nil {
			return nil, fmt.Errorf("storage: open: rebuild SSD catalog: %w", err)
		}
	}

	return newUnit(cfg.Backend, geo, params, mgr, cfg.CatalogPath, cfg.Tuning, hCursor.Prolog.WriteSequence), nil
}

// syncSSDCatalog saves mgr's current snapshot to path, tagged with
// prolog's write_sequence/catalog_time and mgr's current cursor.
func syncSSDCatalog(path string, mgr *index.Manager, prolog fblock.FixedProlog) error {
	meta := SSDCatalogMeta{
		WriteSequence: prolog.WriteSequence,
		CatalogTime:   prolog.CatalogTime,
		Cursor:        mgr.Cursor(),
	}
	return SaveSSDCatalog(path, mgr.Snapshot(), meta)
}
