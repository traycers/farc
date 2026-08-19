package storage

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/index"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storageengine"
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
	pool   *Pool

	// params is read under paramsMu since a future operator API may mutate
	// write_mode/retention.days at runtime (SetWriteMode/SetRetentionDays
	// already exist on index.Manager for the selection algorithm itself;
	// Params here is what gets embedded in the next header written).
	paramsMu sync.RWMutex
	params   fblock.Params

	writeSeq atomic.Uint64
}

func newUnit(backend ioengine.Backend, geo Geometry, params fblock.Params, mgr *index.Manager, catalogPath string, tuning EngineTuning, poolTuning PoolTuning, lastWriteSequence uint64) *Unit {
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
		pool:         newPool(poolTuning),
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
// engine-internal state, so no accessor for this existed before. Metrics
// only, not a live behavioral signal: Pool occupancy is (CONTEXT.md's own
// "Pool" entry) -- every write call site here waits on its own ticket
// before enqueueing the next, so this queue's depth never realistically
// approaches BackpressureAt in current usage.
func (u *Unit) EngineLevel() storageengine.Level { return u.engine.Level() }
func (u *Unit) EngineQueueDepth() int            { return u.engine.QueueDepth() }

// PoolStatus reports the buffer pool's occupancy level — the backpressure
// signal ADR-011 wiring now sources from, superseding EngineLevel.
func (u *Unit) PoolStatus() PoolStatus { return u.pool.Status() }

// PoolSlots reports one row per pool slot (.scratch/fblocks-ui/issues/
// 04-pool-status-list-plan.md's live pool-status list), with
// prolog/catalog/epilog filled from this Storage's *current*
// geometry/params — Storage-wide constants, identical for every fblock at
// this moment, computed fresh here rather than cached on Pool (which has
// no back-reference to Unit). Encodes a throwaway header purely to learn
// its params/catalog byte sizes, mirroring exactly what promoteLocked
// itself computes when a segment is actually promoted.
func (u *Unit) PoolSlots() ([]SlotStatus, error) {
	h := &fblock.Header{Params: u.currentParams(), Catalog: u.mgr.Snapshot()}
	_, err := fblock.EncodeHeader(h)
	if err != nil {
		return nil, fmt.Errorf("storage: pool slots: encode header for current sizes: %w", err)
	}
	defaults := SectionSizes{
		PrologSize:  fblock.FixedPrologSize + h.Prolog.ParamsSize,
		CatalogSize: h.Prolog.CatalogSize,
		EpilogSize:  fblock.EpilogSize,
	}
	return u.pool.Slots(defaults), nil
}

func (u *Unit) currentParams() fblock.Params {
	u.paramsMu.RLock()
	defer u.paramsMu.RUnlock()
	return u.params
}

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
