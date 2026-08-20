package storage

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/index"
)

// HealthMonitor holds simple operation counters and a threshold-based
// bad-ratio alert for one Storage (docs/docs/archive/02-storage.md §4.2.7).
// Per-fblock state counts are derived on demand from IndexManager rather
// than duplicated — IndexManager is the one source of truth for state.
type HealthMonitor struct {
	writes           atomic.Int64
	writeFailures    atomic.Int64
	reads            atomic.Int64
	bytesWritten     atomic.Int64
	fblocksCompleted atomic.Int64

	// fblockSizesMu guards fblockSizes -- RecordFblockSizes is called from
	// the write path, FblockSizes from a Prometheus scrape, potentially
	// concurrently. Keyed by fblock index, last-write-wins: a cyclic
	// storage completes the same physical index repeatedly over its
	// lifetime (the normal steady state, not an edge case), and a
	// Prometheus gather rejects two metrics sharing the same label set in
	// one scrape -- an append-only log would 500 the whole /metrics
	// endpoint the first time any index got reused.
	fblockSizesMu sync.Mutex
	fblockSizes   map[uint32]FblockSizeRecord

	badRatioThreshold float64 // e.g. 0.05 for 5%; <=0 disables the check
	bus               *NotificationBus
}

// FblockSizeRecord is one fblock's catalog/TOC/content section sizes at the
// moment it completed -- farc_fblock_{catalog,toc,content}_size_bytes's
// source (.scratch/storage-fblocks-dashboard-v2/issues/
// 04-fblock-catalog-toc-content-sizes.md). Recorded once, going forward
// only -- no backfill for fblocks already Ready before this shipped.
type FblockSizeRecord struct {
	Index       uint32
	CatalogSize uint32
	TocSize     uint32
	ContentSize uint32
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

// RecordBytesWritten adds n to the running total of fblock content bytes
// successfully written -- farc_storage_bytes_written_total's source
// (.scratch/storage-fblocks-dashboard-v2/issues/
// 02-rtsp-in-vs-storage-write-volume.md), content bytes only, not
// catalog/TOC/prolog/epilog overhead.
func (h *HealthMonitor) RecordBytesWritten(n int) {
	h.bytesWritten.Add(int64(n))
}

// Stats returns the raw operation counters.
func (h *HealthMonitor) Stats() (writes, writeFailures, reads, bytesWritten int64) {
	return h.writes.Load(), h.writeFailures.Load(), h.reads.Load(), h.bytesWritten.Load()
}

// RecordFblockCompleted counts one fblock's transition to Ready --
// farc_fblocks_completed_total's source (.scratch/
// storage-fblocks-dashboard-v2/issues/03-fblock-completion-rate.md), the
// storage's fblock-rotation rate.
func (h *HealthMonitor) RecordFblockCompleted() {
	h.fblocksCompleted.Add(1)
}

// FblocksCompleted returns the running total recorded by
// RecordFblockCompleted.
func (h *HealthMonitor) FblocksCompleted() int64 {
	return h.fblocksCompleted.Load()
}

// RecordFblockSizes appends idx's catalog/TOC/content section sizes,
// recorded once at the exact fblock-completion transition where the caller
// already has them locally -- no disk read.
func (h *HealthMonitor) RecordFblockSizes(idx, catalogSize, tocSize, contentSize uint32) {
	h.fblockSizesMu.Lock()
	defer h.fblockSizesMu.Unlock()
	if h.fblockSizes == nil {
		h.fblockSizes = make(map[uint32]FblockSizeRecord)
	}
	h.fblockSizes[idx] = FblockSizeRecord{
		Index: idx, CatalogSize: catalogSize, TocSize: tocSize, ContentSize: contentSize,
	}
}

// FblockSizes returns the latest size record for every fblock index
// recorded so far, sorted by index -- a stable order for a Prometheus
// scrape's label set.
func (h *HealthMonitor) FblockSizes() []FblockSizeRecord {
	h.fblockSizesMu.Lock()
	defer h.fblockSizesMu.Unlock()
	out := make([]FblockSizeRecord, 0, len(h.fblockSizes))
	for _, rec := range h.fblockSizes {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
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
