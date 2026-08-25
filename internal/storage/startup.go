package storage

import (
	"errors"
	"fmt"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/index"
	"github.com/traycers/farc/internal/ioengine"
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
// v2.0 (ADR-023): delegates to probeGeometryV2 (startup_v2.go).
func probeGeometry(backend ioengine.Backend) (Geometry, error) {
	return probeGeometryV2(backend)
}

// scanForFreshestCatalog is Startup path 2 phases 2-3 (docs/docs/archive/
// 04-storage-operations.md §4.2.2-§4.2.3): read every fblock's fixed prolog,
// rank candidates by write_sequence descending, and return the first
// (highest-write_sequence) one whose catalog node actually validates.
// v2.0 (ADR-023): delegates to scanForFreshestCatalogV2 (startup_v2.go),
// which locates the catalog node via the epilog directory (Count>=3)
// instead of a header CRC.
func scanForFreshestCatalog(backend ioengine.Backend, geo Geometry) (*fblock.Catalog, uint32, error) {
	return scanForFreshestCatalogV2(backend, geo)
}

// OpenConfig configures Startup (docs/docs/archive/04-storage-operations.md
// §4).
type OpenConfig struct {
	Backend     ioengine.Backend
	CatalogPath string // optional SSD catalog mirror (ADR-007)
	Tuning      EngineTuning
	PoolTuning  PoolTuning
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

	// Current operative Params come from the cursor fblock's own prolog —
	// the freshest write's params are the Storage's current ones (operator
	// changes to write_mode/retention.days only take effect in the next
	// write onward, matching every other per-fblock field).
	prolog, params, err := readParamsAndPrologV2(cfg.Backend, geo, cursor)
	if err != nil {
		return nil, fmt.Errorf("storage: open: read cursor fblock %d prolog/params: %w", cursor, err)
	}

	// If the freshest-write_sequence fblock is still Uninitialized in the
	// catalog, it's fblock 0's bootstrap write (Init never counts it as
	// real content) — the write cursor must behave as if nothing has been
	// written yet, so SelectNextIndex's circular walk from cursor+1 starts
	// at index 0 instead of skipping straight past it.
	mgrCursor := cursor
	if cat.State(cursor) == fblock.Uninitialized {
		mgrCursor = geo.N - 1
	}
	mgr := index.New(cat, mgrCursor, params.WriteMode, params.Retention.Days)

	err = ConsistencyCheck(cfg.Backend, geo, mgr)
	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}

	if cfg.CatalogPath != "" {
		err := syncSSDCatalog(cfg.CatalogPath, mgr, prolog.WriteSequence, prolog.CatalogTime)
		if err != nil {
			return nil, fmt.Errorf("storage: open: rebuild SSD catalog: %w", err)
		}
	}

	return newUnit(cfg.Backend, geo, params, mgr, cfg.CatalogPath, cfg.Tuning, cfg.PoolTuning, prolog.WriteSequence), nil
}

// syncSSDCatalog saves mgr's current snapshot to path, tagged with the
// cursor fblock's write_sequence/catalog_time and mgr's current cursor.
func syncSSDCatalog(path string, mgr *index.Manager, writeSequence, catalogTime uint64) error {
	meta := SSDCatalogMeta{
		WriteSequence: writeSequence,
		CatalogTime:   catalogTime,
		Cursor:        mgr.Cursor(),
	}
	return SaveSSDCatalog(path, mgr.Snapshot(), meta)
}
