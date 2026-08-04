package storage

import "traycers/farc/internal/storageengine"

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
