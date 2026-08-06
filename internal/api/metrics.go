package api

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/storage"
	"traycers/farc/internal/storageengine"
)

const nsPerDay = int64(24 * 60 * 60 * 1_000_000_000)

// handleMetrics implements MetricsEndpoint (docs/docs/archive/
// 02-storage.md §8): Prometheus text exposition, hand-rolled since no
// client-library dependency exists in go.mod yet and this is the only
// consumer. Every metric carries storage="<id>", per §8's own convention.
func (s *HttpApiServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for _, info := range s.reg.List() {
		unit, ok := s.reg.Get(info.ID)
		if !ok {
			continue // removed between List() and Get() -- skip rather than error the whole scrape
		}
		writeUnitMetrics(w, info.ID, unit)
	}
}

func writeUnitMetrics(w io.Writer, id string, unit *storage.Unit) {
	geo := unit.Geometry()
	snap := unit.Index().Snapshot()
	retentionNS := uint64(unit.Index().RetentionDays() * nsPerDay)
	now := uint64(time.Now().UnixNano())

	var uninitialized, ready, writable, retained, protectedN, bad, inProgress int
	for i := uint32(0); i < snap.N; i++ {
		switch snap.State(i) {
		case fblock.Uninitialized:
			uninitialized++
		case fblock.Ready:
			ready++
			if snap.Protected(i) {
				protectedN++
			}
			if now-snap.End[i] < retentionNS {
				retained++
			} else if !snap.Protected(i) {
				writable++
			}
		case fblock.Bad:
			bad++
		case fblock.InProgress:
			inProgress++
		}
	}

	used := 0
	for pos := uint16(0); pos < geo.MaxChannels; pos++ {
		if snap.RefCount(pos) > 0 {
			used++
		}
	}

	level := unit.EngineLevel()
	queueStatus := 0
	switch level {
	case storageengine.LevelNormal:
		queueStatus = 0
	case storageengine.LevelWarning:
		queueStatus = 1
	case storageengine.LevelBackpressure:
		queueStatus = 2
	}

	writes, writeFailures, _ := unit.Health().Stats()

	gauge := func(name string, v int) { fmt.Fprintf(w, "%s{storage=%q} %d\n", name, id, v) }
	counter := func(name string, v int64) { fmt.Fprintf(w, "%s{storage=%q} %d\n", name, id, v) }

	gauge("farc_fblocks_total", int(geo.N))
	gauge("farc_fblocks_uninitialized_total", uninitialized)
	gauge("farc_fblocks_ready_total", ready)
	gauge("farc_fblocks_writable_total", writable)
	gauge("farc_fblocks_retained_total", retained)
	gauge("farc_fblocks_protected_total", protectedN)
	gauge("farc_fblocks_bad_total", bad)
	gauge("farc_fblocks_in_progress_total", inProgress)
	gauge("farc_write_queue_depth", unit.EngineQueueDepth())
	gauge("farc_write_queue_status", queueStatus)
	counter("farc_writes_total", writes-writeFailures)
	counter("farc_write_verify_failures_total", writeFailures)
	// farc_reads_in_progress: StorageEngine only counts completed reads
	// (HealthMonitor.RecordRead), not in-flight ones -- exposing 0 rather
	// than inventing an in-flight counter no caller needs yet.
	gauge("farc_reads_in_progress", 0)
	// farc_storage_state: any registered, open Unit is "running" -- v1 has
	// no async job state to report (POST /storages runs Init/Open inline,
	// synchronously, see api.go's package doc), so "job" (2) never applies.
	gauge("farc_storage_state", 1)
	gauge("farc_channel_registry_used", used)
	gauge("farc_channel_registry_capacity", int(geo.MaxChannels))
}
