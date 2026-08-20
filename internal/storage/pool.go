package storage

import (
	"errors"
	"sync"

	"github.com/traycers/farc/toc"
)

// PoolStatus is the buffer pool's fill level (docs/docs/archive/
// 00-requirements.md §4.7's backpressure signal) — occupied-slot count
// against the operator-configured thresholds, PoolTuning. This supersedes
// StorageEngine.Level() as the backpressure source: occupancy (filling,
// queued-unassigned, or actively being written all count as "occupied") is
// the single signal, not something to reconcile with a second,
// StorageEngine-internal queue-depth signal.
type PoolStatus int

const (
	PoolNormal PoolStatus = iota
	PoolWarning
	PoolBackpressure
)

// ErrPoolExhausted is returned by reserve when every pool slot is already
// occupied — the caller (ultimately internal/ingest) decides whether to
// drop frames; the library itself never drops data (00-requirements.md
// §4.7).
var ErrPoolExhausted = errors.New("storage: buffer pool exhausted")

// poolSlot is the minimal thing Pool needs from an occupant to manage
// promotion — kept independent of Segment's own (larger) public surface so
// Pool's FIFO/capacity bookkeeping can be tested in isolation.
type poolSlot interface {
	// promoteLocked assigns this slot a physical index/in_progress state —
	// called synchronously by reserve when the pool was empty, or by
	// release when this slot becomes the new FIFO head. now is Unix ns,
	// needed for index.Manager.SelectNextIndex's retention check; in the
	// backlog case (promotion happening asynchronously, later than the
	// BeginSegment call that created this slot) it's whatever "now" the
	// call that triggered the release (typically a sibling segment's
	// Close) was given, not this slot's own creation time. occupied is
	// the pool's current occupant count (including this slot) — passed
	// in rather than having the implementation call back into Pool, since
	// Pool already holds its own lock across this call and Pool's mutex
	// isn't reentrant. Must not block on anything the pool itself is
	// holding a lock across.
	promoteLocked(now uint64, occupied int) error

	// index returns this slot's assigned physical fblock index and
	// whether one has been assigned yet (promoteLocked has run) — Slots()'s
	// own status snapshot; ok is false for a still-queued slot.
	index() (idx uint32, ok bool)

	// contentBytes returns content bytes committed so far -- physically
	// flushed bytes once promoted (WriteHandle.Written() minus the
	// header/magic prefix), or the in-memory backlog length before
	// promotion. Lock-free for the same reason as index()/closing().
	contentBytes() int64

	// elementCount returns the number of tree nodes appended so far --
	// the sole input to toc.EncodedSize's exact closed-form TOC-size
	// formula (Slots() never calls toc.Build while filling).
	elementCount() int

	// closing reports whether Close is currently in flight on this slot —
	// Slots()'s SlotClosing signal. Must not block on the slot's own
	// mutex: segmentImpl.Close holds that lock for its entire body
	// (including the disk write it waits on), so a mutex-guarded getter
	// here would just stall until Close finishes instead of ever
	// observing the in-flight state — the implementation backs this with
	// a lock-free flag instead.
	closing() bool
}

// Pool holds up to `size` segments that may be filling, queued (fully
// filled but with no physical index yet), or actively being written at
// once — but only the FIFO head (occupied[0]) ever holds an assigned
// physical index: the one actually being written to disk right now.
// Everything else waits its turn with no physical position at all (the
// fblocks-grid page's "?" square).
type Pool struct {
	mu       sync.Mutex
	size     int
	warnAt   int
	pressAt  int
	occupied []poolSlot
}

// newPool builds a Pool from tuning (zero fields default via withDefaults).
func newPool(tuning PoolTuning) *Pool {
	t := tuning.withDefaults()
	return &Pool{size: t.Size, warnAt: t.WarningAt, pressAt: t.BackpressureAt}
}

// Tuning reports the resolved PoolTuning this Pool was constructed with
// (defaults already applied by newPool) -- internal/api's GET /storages
// uses this at Storage-open time to learn what's actually in effect.
func (p *Pool) Tuning() PoolTuning {
	return PoolTuning{Size: p.size, WarningAt: p.warnAt, BackpressureAt: p.pressAt}
}

// Status reports the pool's current occupancy level.
func (p *Pool) Status() PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusLocked()
}

// SlotState is one pool slot's fill state (fblocks-status live pool list,
// .scratch/fblocks-ui/issues/04-pool-status-list-plan.md).
type SlotState int

const (
	SlotFree SlotState = iota
	// SlotQueued is a reserved, non-head slot (occupied[i], i>0):
	// accumulating in memory, no physical index yet.
	SlotQueued
	// SlotActive is the FIFO head (occupied[0]): has a physical index,
	// genuinely in_progress on disk.
	SlotActive
	// SlotClosing is the FIFO head with Close in flight: the final
	// TOC+epilog write (including the corruption-triggered fresh-index
	// retry loop). Overrides SlotActive once closing() reports true.
	SlotClosing
)

// SectionSizes is the byte size of the three fblock sections that are
// Storage-wide constants, identical for every fblock in a Storage at a
// given moment (docs/docs/archive/03-storage-format.md) — not per-slot
// state, so the caller (Unit, which owns geometry/params) computes these
// fresh on each Slots() call rather than Pool holding a back-reference to
// it (.scratch/fblocks-ui/issues/04-pool-status-list-plan.md).
type SectionSizes struct {
	PrologSize  uint32
	CatalogSize uint32
	EpilogSize  uint32
}

// SlotStatus is one row of Pool.Slots(). PrologSize/CatalogSize/EpilogSize
// are defaults, denormalized as-is into every row (free rows included) so
// a free row renders at the same expected section sizes as an occupied
// one, per the design's "same visual structure, not a cut-down shape".
type SlotStatus struct {
	State    SlotState
	Index    uint32
	HasIndex bool

	PrologSize  uint32
	CatalogSize uint32
	ContentSize int64
	TOCSize     uint32
	EpilogSize  uint32
}

// Slots returns exactly `size` entries (PoolTuning.Size), one per pool
// slot in FIFO order, including free (unreserved) ones.
func (p *Pool) Slots(defaults SectionSizes) []SlotStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]SlotStatus, p.size)
	for i := range out {
		out[i].PrologSize = defaults.PrologSize
		out[i].CatalogSize = defaults.CatalogSize
		out[i].EpilogSize = defaults.EpilogSize
		if i >= len(p.occupied) {
			out[i].State = SlotFree
			out[i].TOCSize = toc.EncodedSize(0)
			continue
		}
		slot := p.occupied[i]
		idx, ok := slot.index()
		state := SlotQueued
		if i == 0 {
			state = SlotActive
			if slot.closing() {
				state = SlotClosing
			}
		}
		out[i].State = state
		out[i].Index = idx
		out[i].HasIndex = ok
		out[i].ContentSize = slot.contentBytes()
		out[i].TOCSize = toc.EncodedSize(uint32(slot.elementCount()))
	}
	return out
}

func (p *Pool) statusLocked() PoolStatus {
	n := len(p.occupied)
	switch {
	case n >= p.pressAt:
		return PoolBackpressure
	case n >= p.warnAt:
		return PoolWarning
	default:
		return PoolNormal
	}
}

// reserve claims a slot for seg. If the pool was empty, seg becomes the
// FIFO head and is promoted synchronously (the steady-state, no-backlog
// case — index assignment coincides with fill-start). Returns
// ErrPoolExhausted if every slot is already occupied.
func (p *Pool) reserve(seg poolSlot, now uint64) (PoolStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.occupied) >= p.size {
		return p.statusLocked(), ErrPoolExhausted
	}
	wasEmpty := len(p.occupied) == 0
	p.occupied = append(p.occupied, seg)
	if wasEmpty {
		err := seg.promoteLocked(now, len(p.occupied))
		if err != nil {
			p.occupied = p.occupied[:len(p.occupied)-1]
			return p.statusLocked(), err
		}
	}
	return p.statusLocked(), nil
}

// release removes seg — which must be the current FIFO head, since only the
// head is ever actively closing/finishing — and promotes the next queued
// segment, if any (the backlog case: index assignment happens here, once
// the previously-active segment's own write finished).
func (p *Pool) release(seg poolSlot, now uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.occupied) == 0 || p.occupied[0] != seg {
		return errors.New("storage: pool: release called on a non-head segment")
	}
	p.occupied = p.occupied[1:]
	if len(p.occupied) > 0 {
		return p.occupied[0].promoteLocked(now, len(p.occupied))
	}
	return nil
}
