package storage

import (
	"context"
	"sync"
	"sync/atomic"

	"traycers/farc/fblock"
	"traycers/farc/internal/index"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storageengine"
)

// DefaultBadRatioThreshold is HealthMonitor's default bad-fblock-ratio
// alert threshold — 02-storage.md §4.2.7's own worked example ("доля
// битых превысила 5%").
const DefaultBadRatioThreshold = 0.05

// Unit is StorageUnit (docs/docs/archive/02-storage.md §4.2): one open
// Storage, wiring together IndexManager, StorageEngine, the SSD catalog
// mirror, NotificationBus and HealthMonitor. Recorder/Reader (recorder.go,
// reader.go) are methods on Unit rather than separate types — every
// operation they need (backend, geometry, IndexManager, StorageEngine,
// notify/health, the current Params) already lives here, and Unit is the
// only thing ever constructed (via Open), so splitting them out would only
// add constructors that thread the same shared state through twice.
type Unit struct {
	backend     ioengine.Backend
	geo         Geometry
	catalogPath string

	engine       *storageengine.Engine
	engineCancel context.CancelFunc
	engineDone   chan struct{}

	mgr    *index.Manager
	notify *NotificationBus
	health *HealthMonitor

	// writeMu serializes WriteFcontainer calls — Recorder is documented as
	// single-writer per Storage (02-storage.md §0), so only one
	// select-index/build-header/write sequence runs at a time.
	writeMu sync.Mutex

	// params is read under paramsMu since a future operator API may mutate
	// write_mode/retention.days at runtime (SetWriteMode/SetRetentionDays
	// already exist on index.Manager for the selection algorithm itself;
	// Params here is what gets embedded in the next header written).
	paramsMu sync.RWMutex
	params   fblock.Params

	writeSeq atomic.Uint64
}

func newUnit(backend ioengine.Backend, geo Geometry, params fblock.Params, mgr *index.Manager, catalogPath string, tuning EngineTuning, lastWriteSequence uint64) *Unit {
	eng := storageengine.New(backend, engineConfig(params.FchunkSize, params.ReadChunkSize, tuning))
	ctx, cancel := context.WithCancel(context.Background())

	u := &Unit{
		backend:      backend,
		geo:          geo,
		catalogPath:  catalogPath,
		engine:       eng,
		engineCancel: cancel,
		engineDone:   make(chan struct{}),
		mgr:          mgr,
		params:       params,
	}
	u.notify = NewNotificationBus()
	u.health = NewHealthMonitor(u.notify, DefaultBadRatioThreshold)
	u.writeSeq.Store(lastWriteSequence)

	go func() {
		defer close(u.engineDone)
		eng.Run(ctx)
	}()
	return u
}

// Geometry returns the Storage's fixed shape.
func (u *Unit) Geometry() Geometry { return u.geo }

// Notify returns the Storage's NotificationBus.
func (u *Unit) Notify() *NotificationBus { return u.notify }

// Health returns the Storage's HealthMonitor.
func (u *Unit) Health() *HealthMonitor { return u.health }

// Index returns the Storage's IndexManager, for read-only queries
// (ResolveUUID/Candidates) or operator mutations (SetWriteMode etc.).
func (u *Unit) Index() *index.Manager { return u.mgr }

// EngineLevel and EngineQueueDepth expose StorageEngine's write-queue fill
// state (ADR-011) for MetricsEndpoint (farc_write_queue_depth/status,
// docs/docs/archive/02-storage.md §8) — the only Phase 10 consumer of
// engine-internal state, so no accessor for this existed before.
func (u *Unit) EngineLevel() storageengine.Level { return u.engine.Level() }
func (u *Unit) EngineQueueDepth() int            { return u.engine.QueueDepth() }

func (u *Unit) currentParams() fblock.Params {
	u.paramsMu.RLock()
	defer u.paramsMu.RUnlock()
	return u.params
}

// MinContainerShare returns the storage's configured minimum fcontainer
// share (fblock/params.go's Params.MinContainerShare) — the fraction of
// fblock_size a single fcontainer is expected to fill. Exposed publicly (a
// symmetric counterpart to Geometry) so internal/farcd can size
// internal/ingest's shared-segment flush target off it without reaching
// into currentParams, which stays private since nothing else outside this
// package needs the whole Params value.
func (u *Unit) MinContainerShare() float64 { return u.currentParams().MinContainerShare }

func (u *Unit) nextWriteSequence() uint64 {
	return u.writeSeq.Add(1)
}

// Close stops the engine's background loop and releases the backend. No
// WriteFcontainer/Read* call may be in flight when Close is called.
func (u *Unit) Close() error {
	u.engineCancel()
	<-u.engineDone
	return u.engine.Close()
}
