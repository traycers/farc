package storage

import (
	"fmt"

	"github.com/traycers/farc/internal/storageengine"
)

// EngineTuning are the storageengine.Config knobs that aren't part of the
// on-disk fblock.Params JSON (docs/docs/archive/03-storage-format.md §5.2
// only defines fchunk_size/read_chunk_size/write_mode/retention/
// min_container_share) — ADR-011's own text leaves M/K "fixed at
// implementation time", and the warning/backpressure queue-length
// thresholds have no documented values at all. Callers may override any
// field; zero fields fall back to DefaultEngineTuning.
type EngineTuning struct {
	WarningAt      int
	BackpressureAt int
	QuotaEvery     int
	QuotaPortions  int
}

// DefaultEngineTuning returns this package's chosen v1 defaults: ADR-011's
// own worked example for M/K, and small write-job-count thresholds for
// warning/backpressure (a Storage has one Recorder, so its write queue is
// expected to be shallow in normal operation).
func DefaultEngineTuning() EngineTuning {
	return EngineTuning{
		WarningAt:      4,
		BackpressureAt: 16,
		QuotaEvery:     storageengine.DefaultQuotaEvery,
		QuotaPortions:  storageengine.DefaultQuotaPortions,
	}
}

func (t EngineTuning) withDefaults() EngineTuning {
	d := DefaultEngineTuning()
	if t.WarningAt == 0 {
		t.WarningAt = d.WarningAt
	}
	if t.BackpressureAt == 0 {
		t.BackpressureAt = d.BackpressureAt
	}
	if t.QuotaEvery == 0 {
		t.QuotaEvery = d.QuotaEvery
	}
	if t.QuotaPortions == 0 {
		t.QuotaPortions = d.QuotaPortions
	}
	return t
}

// PoolTuning are Pool's occupancy-threshold knobs — pure in-memory
// operational parameters (same convention as EngineTuning above), not part
// of the on-disk fblock.Params JSON. Callers may override any field; zero
// fields fall back to DefaultPoolTuning.
type PoolTuning struct {
	Size           int
	WarningAt      int
	BackpressureAt int
}

// DefaultPoolTuning returns this package's chosen v1 defaults.
func DefaultPoolTuning() PoolTuning {
	return PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4}
}

func (t PoolTuning) withDefaults() PoolTuning {
	d := DefaultPoolTuning()
	if t.Size == 0 {
		t.Size = d.Size
	}
	if t.WarningAt == 0 {
		t.WarningAt = d.WarningAt
	}
	if t.BackpressureAt == 0 {
		t.BackpressureAt = d.BackpressureAt
	}
	return t
}

// Resolved returns t with every zero field replaced by
// DefaultPoolTuning's -- the actual values a caller who only specified part
// of the group (e.g. just Size) ends up with. Callers that persist or cache
// a PoolTuning after validating it (PATCH /storages/{id}'s pool group,
// POST /storages before storage.Open resolves it internally) must store
// this, not the raw, possibly-partial input, or a stored zero field would
// misrepresent an in-effect default as "0" instead of what it actually
// resolves to.
func (t PoolTuning) Resolved() PoolTuning { return t.withDefaults() }

// Validate reports whether t's fully-resolved (default-applied) values
// satisfy 1 <= WarningAt <= BackpressureAt <= Size -- the ordering
// backpressure's occupancy check (Pool.statusLocked) assumes.
func (t PoolTuning) Validate() error {
	r := t.withDefaults()
	if r.WarningAt < 1 || r.BackpressureAt < r.WarningAt || r.Size < r.BackpressureAt {
		return fmt.Errorf("storage: pool tuning must satisfy 1 <= warning_at <= backpressure_at <= size (got size=%d warning_at=%d backpressure_at=%d)", r.Size, r.WarningAt, r.BackpressureAt)
	}
	return nil
}

func engineConfig(fchunkSize, readChunkSize int64, tuning EngineTuning) storageengine.Config {
	tuning = tuning.withDefaults()
	return storageengine.Config{
		FchunkSize:     fchunkSize,
		ReadChunkSize:  readChunkSize,
		WarningAt:      tuning.WarningAt,
		BackpressureAt: tuning.BackpressureAt,
		QuotaEvery:     tuning.QuotaEvery,
		QuotaPortions:  tuning.QuotaPortions,
	}
}
