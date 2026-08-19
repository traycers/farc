package api

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/internal/storageengine"
)

const nsPerDay = int64(24 * 60 * 60 * 1_000_000_000)

// Metric descriptors for storageCollector, docs/docs/archive/02-storage.md
// §4.1.3/§8: every metric carries a storage="<id>" label.
var (
	fblocksTotalDesc            = prometheus.NewDesc("farc_fblocks_total", "Total fblocks in the storage.", []string{"storage"}, nil)
	fblocksUninitializedDesc    = prometheus.NewDesc("farc_fblocks_uninitialized_total", "Fblocks still in the Uninitialized state.", []string{"storage"}, nil)
	fblocksReadyDesc            = prometheus.NewDesc("farc_fblocks_ready_total", "Fblocks in the Ready state.", []string{"storage"}, nil)
	fblocksWritableDesc         = prometheus.NewDesc("farc_fblocks_writable_total", "Ready fblocks past retention and unprotected -- eligible for the writer to reuse.", []string{"storage"}, nil)
	fblocksRetainedDesc         = prometheus.NewDesc("farc_fblocks_retained_total", "Ready fblocks still within their retention window.", []string{"storage"}, nil)
	fblocksProtectedDesc        = prometheus.NewDesc("farc_fblocks_protected_total", "Ready fblocks marked protected (read-only).", []string{"storage"}, nil)
	fblocksBadDesc              = prometheus.NewDesc("farc_fblocks_bad_total", "Fblocks in the Bad state.", []string{"storage"}, nil)
	fblocksInProgressDesc       = prometheus.NewDesc("farc_fblocks_in_progress_total", "Fblocks currently being written.", []string{"storage"}, nil)
	writeQueueDepthDesc         = prometheus.NewDesc("farc_write_queue_depth", "Current depth of the StorageEngine write queue.", []string{"storage"}, nil)
	writeQueueStatusDesc        = prometheus.NewDesc("farc_write_queue_status", "Write queue backpressure status: 0=normal, 1=warning, 2=backpressure.", []string{"storage"}, nil)
	writesTotalDesc             = prometheus.NewDesc("farc_writes_total", "Total successful writes.", []string{"storage"}, nil)
	writeVerifyFailuresDesc     = prometheus.NewDesc("farc_write_verify_failures_total", "Total write-verify failures.", []string{"storage"}, nil)
	readsInProgressDesc         = prometheus.NewDesc("farc_reads_in_progress", "Reads currently in progress (always 0 today -- HealthMonitor only counts completed reads).", []string{"storage"}, nil)
	storageStateDesc            = prometheus.NewDesc("farc_storage_state", "Storage state: 1=running (v1 has no async job state).", []string{"storage"}, nil)
	channelRegistryUsedDesc     = prometheus.NewDesc("farc_channel_registry_used", "Occupied channel registry slots.", []string{"storage"}, nil)
	channelRegistryCapacityDesc = prometheus.NewDesc("farc_channel_registry_capacity", "Total channel registry slots.", []string{"storage"}, nil)
)

// storageCollector implements prometheus.Collector: Prometheus text
// exposition for every registered Storage, hand-computed from live state at
// scrape time (docs/docs/archive/02-storage.md §8: no history is kept, an
// external system like Prometheus/VictoriaMetrics owns aggregation).
type storageCollector struct {
	reg *StorageRegistry
}

func (c *storageCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		fblocksTotalDesc, fblocksUninitializedDesc, fblocksReadyDesc, fblocksWritableDesc,
		fblocksRetainedDesc, fblocksProtectedDesc, fblocksBadDesc, fblocksInProgressDesc,
		writeQueueDepthDesc, writeQueueStatusDesc, writesTotalDesc, writeVerifyFailuresDesc,
		readsInProgressDesc, storageStateDesc, channelRegistryUsedDesc, channelRegistryCapacityDesc,
	} {
		ch <- d
	}
}

func (c *storageCollector) Collect(ch chan<- prometheus.Metric) {
	for _, info := range c.reg.List() {
		unit, ok := c.reg.Get(info.ID)
		if !ok {
			continue // removed between List() and Get() -- skip rather than error the whole scrape
		}
		collectUnitMetrics(ch, info.ID, unit)
	}
}

func collectUnitMetrics(ch chan<- prometheus.Metric, id string, unit *storage.Unit) {
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

	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, id)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, id)
	}

	gauge(fblocksTotalDesc, float64(geo.N))
	gauge(fblocksUninitializedDesc, float64(uninitialized))
	gauge(fblocksReadyDesc, float64(ready))
	gauge(fblocksWritableDesc, float64(writable))
	gauge(fblocksRetainedDesc, float64(retained))
	gauge(fblocksProtectedDesc, float64(protectedN))
	gauge(fblocksBadDesc, float64(bad))
	gauge(fblocksInProgressDesc, float64(inProgress))
	gauge(writeQueueDepthDesc, float64(unit.EngineQueueDepth()))
	gauge(writeQueueStatusDesc, float64(queueStatus))
	counter(writesTotalDesc, float64(writes-writeFailures))
	counter(writeVerifyFailuresDesc, float64(writeFailures))
	// farc_reads_in_progress: StorageEngine only counts completed reads
	// (HealthMonitor.RecordRead), not in-flight ones -- exposing 0 rather
	// than inventing an in-flight counter no caller needs yet.
	gauge(readsInProgressDesc, 0)
	// farc_storage_state: any registered, open Unit is "running" -- v1 has
	// no async job state to report (POST /storages runs Init/Open inline,
	// synchronously, see api.go's package doc), so "job" (2) never applies.
	gauge(storageStateDesc, 1)
	gauge(channelRegistryUsedDesc, float64(used))
	gauge(channelRegistryCapacityDesc, float64(geo.MaxChannels))
}
