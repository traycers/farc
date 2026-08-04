package storage

import (
	"sync/atomic"

	"traycers/farc/fblock"
	"traycers/farc/internal/index"
)

// HealthMonitor holds simple operation counters and a threshold-based
// bad-ratio alert for one Storage (docs/docs/archive/02-storage.md §4.2.7).
// Per-fblock state counts are derived on demand from IndexManager rather
// than duplicated — IndexManager is the one source of truth for state.
type HealthMonitor struct {
	writes        atomic.Int64
	writeFailures atomic.Int64
	reads         atomic.Int64

	badRatioThreshold float64 // e.g. 0.05 for 5%; <=0 disables the check
	bus               *NotificationBus
}

// NewHealthMonitor creates a HealthMonitor publishing storage.alert events
// to bus when CheckBadRatio finds the bad/total ratio at or above
// badRatioThreshold.
func NewHealthMonitor(bus *NotificationBus, badRatioThreshold float64) *HealthMonitor {
	return &HealthMonitor{bus: bus, badRatioThreshold: badRatioThreshold}
}

// RecordWrite counts one completed write attempt (Recorder).
func (h *HealthMonitor) RecordWrite(failed bool) {
	h.writes.Add(1)
	if failed {
		h.writeFailures.Add(1)
	}
}

// RecordRead counts one completed read (Reader).
func (h *HealthMonitor) RecordRead() {
	h.reads.Add(1)
}

// Stats returns the raw operation counters.
func (h *HealthMonitor) Stats() (writes, writeFailures, reads int64) {
	return h.writes.Load(), h.writeFailures.Load(), h.reads.Load()
}

// Counts returns the current number of fblocks in each state.
func (h *HealthMonitor) Counts(mgr *index.Manager) map[fblock.State]int {
	cat := mgr.Snapshot()
	counts := make(map[fblock.State]int, 4)
	for i := uint32(0); i < cat.N; i++ {
		counts[cat.State(i)]++
	}
	return counts
}

// CheckBadRatio recomputes the bad/total fblock ratio and publishes a
// storage.alert if it's at or above badRatioThreshold (§4.2.7's own
// example: "доля битых превысила 5%").
func (h *HealthMonitor) CheckBadRatio(mgr *index.Manager) {
	if h.badRatioThreshold <= 0 {
		return
	}
	counts := h.Counts(mgr)
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return
	}
	ratio := float64(counts[fblock.Bad]) / float64(total)
	if ratio >= h.badRatioThreshold {
		h.bus.Publish(Event{Name: EventStorageAlert, Severity: "critical", Reason: AlertBadRatioExceeded})
	}
}
