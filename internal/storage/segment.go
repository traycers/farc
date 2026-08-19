package storage

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/storageengine"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// ErrSegmentClosed is returned by AddStreamParams/AddFrames/RegisterChannel
// once Close has already been called (or the pool has otherwise closed
// this segment out from under a stale caller by fullness) — callers must
// react by requesting a fresh segment via BeginSegment, not retry the same
// handle.
var ErrSegmentClosed = errors.New("storage: segment already closed")

// Segment is one open fcontainer being filled — mirroring
// fcontainer.Filler's own producer API exactly, so a caller with a
// fully-built Filler-worth of calls needs no different shape to talk to a
// shared, pool-managed segment instead (this is exactly what lets
// internal/ingest's ticket route every channel of a Storage into one
// shared segment without a bespoke API).
type Segment interface {
	AddStreamParams(channel, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (uint32, error)
	AddFrames(configID uint32, frames []fcontainer.Frame) error

	// RegisterChannel lets a channel join an already-open segment
	// mid-flight — resolves a compact position and sets this fblock's
	// channel_bitmap bit (once a physical index is assigned), mirroring
	// WriteFcontainer's RegisterChannels/SetChannelBit pairing.
	RegisterChannel(channel uint16) error

	// Elements is Filler.Elements()'s equivalent — the in-progress tree,
	// merged across every channel writing to this segment so far.
	Elements() []mediatree.Element

	// Close finalizes the segment: builds the real TOC from the full
	// tree, writes the tail (whatever content wasn't yet periodically
	// flushed, zero-padded to capacity) + magic_toc + TOC + epilog,
	// retrying on corruption with a fresh index exactly like today's
	// WriteFcontainer, then commits + notifies + releases the pool slot
	// (promoting the next queued segment, if any). now is Unix ns.
	// Close takes no begin/end — the segment tracks its own running
	// min(begin)/max(end) internally, updated on every AddFrames call
	// regardless of which caller/channel made it, since no single caller
	// of Close knows the true combined range once multiple channels share
	// one segment.
	Close(now uint64) ([16]byte, error)
}

// segmentImpl is Segment's only implementation.
type segmentImpl struct {
	mu     sync.Mutex
	unit   *Unit
	filler *fcontainer.Filler

	positions   map[uint16]uint16 // channel number -> compact registry position
	sentElemIdx int               // Filler.ElementsFrom high-water mark
	backlog     []byte            // encoded, not-yet-Appended content bytes, while not yet promoted
	contentLen  int64             // total encoded content bytes ever added (sum of every pushReadyLocked tail) -- unlike contentBytes()/Written(), not lagged by the async periodic-flush engine goroutine; isFullLocked's own source of truth, since Close()'s eventual full mediatree.EncodeContent(elems) is exactly this same running total by construction (ADR-017 already relies on incremental tail encodings concatenating byte-for-byte into the same result as encoding everything at once)

	uuid        [16]byte
	idx         uint32
	promoted    bool
	handle      *storageengine.WriteHandle
	headerLen   int64 // len(headerAndMagic) -- WriteHandle.Written()'s baseline
	paramsSize  uint32
	catalogSize uint32
	writeSeq    uint64

	begin, end uint64
	haveFrame  bool
	closed     bool

	// closingFlag backs poolSlot.closing() — set before s.mu is taken so
	// it stays observable for Close's entire body, which holds s.mu
	// locked throughout (including the disk write it waits on).
	closingFlag atomic.Bool

	// idxAtomic/promotedAtomic mirror s.idx/s.promoted for poolSlot's
	// index() — lock-free for the same reason as closingFlag: Close holds
	// s.mu for its entire body, so a mutex-guarded read here would stall a
	// live status query for the whole close instead of ever observing it.
	// Kept in sync at every s.idx/s.promoted assignment site.
	idxAtomic      atomic.Uint32
	promotedAtomic atomic.Bool

	// backlogLenAtomic mirrors len(s.backlog) lock-free, for
	// contentBytes()'s pre-promotion case -- updated at the same site
	// that mutates s.backlog under s.mu.
	backlogLenAtomic atomic.Int64
}

// BeginSegment reserves the next pool slot for channels (00-requirements.md
// §4.8 step 1: "get a buffer"). Returns the segment handle, the pool's
// current occupancy status, and this Storage's approximate max content
// size (a conservative upper bound — the real capacity is slightly
// smaller once a real TOC exists). Never blocks: returns ErrPoolExhausted
// if every pool slot is occupied — the caller decides whether to drop
// frames, per 00-requirements.md §4.7 ("library never drops, only
// signals"). now is Unix ns, needed if this segment ends up promoted
// synchronously (pool was empty).
func (u *Unit) BeginSegment(channels []uint16, now uint64) (Segment, PoolStatus, int64, error) {
	uuid, err := newUUIDv4()
	if err != nil {
		return nil, PoolNormal, 0, err
	}
	seg := &segmentImpl{
		unit:      u,
		filler:    fcontainer.New(),
		positions: make(map[uint16]uint16),
		uuid:      uuid,
	}
	// Register the initial channel set before reserving a pool slot, so
	// that whenever promotion actually happens (synchronously here, or
	// later under backlog) s.positions already reflects them and their
	// channel_bitmap bits get set as part of promotion itself.
	for _, ch := range channels {
		err := seg.RegisterChannel(ch)
		if err != nil {
			return nil, PoolNormal, 0, err
		}
	}
	status, err := u.pool.reserve(seg, now)
	if err != nil {
		return nil, status, 0, err
	}
	return seg, status, u.contentCapacityEstimate(), nil
}

// contentCapacityEstimate builds a throwaway header from the current
// catalog snapshot purely to learn what ParamsSize/CatalogSize would be
// (fblock.EncodeHeader's side effect), then returns the resulting content
// capacity with toc_size=0 -- an upper bound, not the exact final capacity
// (a real TOC is never zero-sized once a segment has any content).
func (u *Unit) contentCapacityEstimate() int64 {
	h := &fblock.Header{
		Prolog:  fblock.FixedProlog{FblockSize: u.geo.FblockSize, MaxChannels: u.geo.MaxChannels},
		Params:  u.currentParams(),
		Catalog: u.mgr.Snapshot(),
	}
	_, err := fblock.EncodeHeader(h)
	if err != nil {
		return 0
	}
	return fblock.ContentSize(h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, 0, u.backend.Alignment())
}

// promoteLocked implements poolSlot -- called by Pool while holding Pool's
// own lock (not segmentImpl's), so it acquires s.mu itself: a segment
// returned to its caller while still queued (backlog case) can have
// AddStreamParams/AddFrames/RegisterChannel called on it concurrently with
// promotion happening asynchronously, once a sibling segment's Close
// releases the pool slot.
// index implements poolSlot -- Pool.Slots()'s own status snapshot.
func (s *segmentImpl) index() (uint32, bool) {
	return s.idxAtomic.Load(), s.promotedAtomic.Load()
}

// closing implements poolSlot -- lock-free since Close holds s.mu locked
// for its entire body.
func (s *segmentImpl) closing() bool {
	return s.closingFlag.Load()
}

// contentBytes implements poolSlot -- lock-free, same reasoning as
// index()/closing(). Safe to read s.handle/s.headerLen once
// promotedAtomic reports true: promoteLocked publishes them (in program
// order) strictly before setting that flag.
func (s *segmentImpl) contentBytes() int64 {
	if s.promotedAtomic.Load() {
		return s.handle.Written() - s.headerLen
	}
	return s.backlogLenAtomic.Load()
}

// elementCount implements poolSlot -- Filler.Len() has its own internal
// locking independent of s.mu, so this is already lock-free relative to
// a Close in flight.
func (s *segmentImpl) elementCount() int {
	return s.filler.Len()
}

func (s *segmentImpl) promoteLocked(now uint64, occupied int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u := s.unit
	idx, h, err := u.beginFblockWrite(now, s.uuid, s.positions, s.begin, s.end)
	if err != nil {
		return err
	}

	headerAndMagic, err := assembleHeaderAndMagic(h, u.backend.Alignment())
	if err != nil {
		return fmt.Errorf("storage: segment: assemble header for fblock %d: %w", idx, err)
	}

	s.idx = idx
	s.promoted = true
	s.headerLen = int64(len(headerAndMagic))
	s.paramsSize = h.Prolog.ParamsSize
	s.catalogSize = h.Prolog.CatalogSize
	s.writeSeq = h.Prolog.WriteSequence

	timeout := time.Duration(h.Params.FlushTimeoutNS) * time.Nanosecond
	// Under backlog (more than this one segment queued in the pool),
	// ADR-017's timeout is ignored entirely -- write as fast as possible
	// to drain the backlog, paced by fchunk_size alone.
	if occupied > 1 {
		timeout = 0
	}
	s.handle = u.engine.EnqueueOpenWrite(int64(fblockOffset(u.geo, idx)), headerAndMagic, timeout)
	if len(s.backlog) > 0 {
		err = s.handle.Append(s.backlog)
		if err != nil {
			return err
		}
		s.backlog = nil
	}

	// Published last, after s.handle/s.headerLen are fully set: poolSlot's
	// lock-free readers (index(), and anything gated on promoted) key off
	// these atomics to know it's now safe to read s.handle/s.headerLen
	// without taking s.mu -- the standard safe-publication pattern (Go
	// memory model: a happens-before edge through the atomic store/load).
	s.idxAtomic.Store(idx)
	s.promotedAtomic.Store(true)
	return nil
}

func (s *segmentImpl) AddStreamParams(channel, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrSegmentClosed
	}
	if s.isFullLocked() {
		return 0, s.closeForFullnessLocked(params.Time)
	}
	cid, err := s.filler.AddStreamParams(channel, stream, kind, params)
	if err != nil {
		return 0, err
	}
	err = s.pushReadyLocked(params.Time)
	if err != nil {
		return 0, err
	}
	return cid, nil
}

func (s *segmentImpl) AddFrames(configID uint32, frames []fcontainer.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSegmentClosed
	}
	if s.isFullLocked() {
		return s.closeForFullnessLocked(frames[len(frames)-1].Time)
	}
	err := s.filler.AddFrames(configID, frames)
	if err != nil {
		return err
	}
	for _, fr := range frames {
		s.trackTimeLocked(fr.Time)
	}
	return s.pushReadyLocked(frames[len(frames)-1].Time)
}

// isFullLocked reports whether s has already reached this fblock's live
// content capacity -- fblock.ContentSize using the current element count's
// exact TOC size (toc.EncodedSize is exact given a row count, not
// approximate), the same estimate pool.go's Slots() already computes for
// the pool-status-list bar's own live TOCSize display. Only meaningful
// once promoted: a still-queued/backlogged segment (paramsSize/catalogSize
// not assigned yet) has no physical fblock to compare against, so it's
// never considered full here -- the real check happens once it's promoted
// and these fields are set (.scratch/multi-channel-fcontainer/issues/
// 03-no-fullness-driven-fblock-rotation.md).
func (s *segmentImpl) isFullLocked() bool {
	if !s.promoted {
		return false
	}
	tocEst := toc.EncodedSize(uint32(s.filler.Len()))
	capacity := fblock.ContentSize(s.unit.geo.FblockSize, s.paramsSize, s.catalogSize, tocEst, s.unit.backend.Alignment())
	// A margin of one fchunk_size reserves headroom for whatever single
	// batch triggers the close -- Filler has no rollback, so that batch is
	// already irrevocably in s.filler by the time this fblock is deemed
	// full (proactive-but-stale-state check, agreed during grilling).
	// Without this margin, a single large batch (e.g. a prerecord-replay
	// burst) could push real content past the fblock's own physical
	// capacity, hitting writeTailLocked's hard "content exceeds capacity"
	// error instead of a clean close -- reusing fchunk_size (already a
	// configured, meaningful "unit of write work" for this Storage) avoids
	// inventing a new margin constant.
	margin := s.unit.currentParams().FchunkSize
	return s.contentLen+margin >= capacity
}

// closeForFullnessLocked finalizes s because it has reached capacity --
// unlike failLocked (a write error, ending in fblock state Bad), reaching
// capacity is a *successful* close (real TOC/epilog, fblock state Ready),
// so it reuses closeLocked directly rather than failLocked's shape. Always
// returns ErrSegmentClosed on success, exactly like failLocked, so the
// caller (internal/ingest's StorageSegment.call) retries transparently
// against a freshly opened segment -- any real error from closeLocked
// itself is propagated instead, not masked.
func (s *segmentImpl) closeForFullnessLocked(now uint64) error {
	s.closed = true
	_, err := s.closeLocked(now)
	if err != nil {
		return err
	}
	return ErrSegmentClosed
}

func (s *segmentImpl) trackTimeLocked(t uint64) {
	if !s.haveFrame {
		s.begin, s.end = t, t
		s.haveFrame = true
		return
	}
	if t < s.begin {
		s.begin = t
	}
	if t > s.end {
		s.end = t
	}
}

// pushReadyLocked encodes whatever's new since the last call
// (Filler.ElementsFrom, O(new elements)) and hands it to the engine
// (already-promoted case) or buffers it locally (still-queued case, no
// physical index/WriteHandle to Append to yet). now is used only if the
// underlying engine job already failed (failLocked's fblock-Bad/pool
// bookkeeping needs a timestamp) — the caller's own frame/params time
// doubles as "now" here, same convention as internal/ingest/policy.go's
// HandleFrame.
func (s *segmentImpl) pushReadyLocked(now uint64) error {
	tail, upto := s.filler.ElementsFrom(s.sentElemIdx)
	if len(tail) == 0 {
		return nil
	}
	s.sentElemIdx = upto
	newBytes := mediatree.EncodeContent(tail)
	s.contentLen += int64(len(newBytes))
	if s.promoted {
		err := s.handle.Append(newBytes)
		if err != nil {
			return s.failLocked(now)
		}
	} else {
		s.backlog = append(s.backlog, newBytes...)
		s.backlogLenAtomic.Store(int64(len(s.backlog)))
	}
	return nil
}

// failLocked reacts to the underlying engine write job having already
// failed out from under this open segment (storageengine.ErrWriteJobFailed
// or a real I/O error, surfaced via WriteHandle.Append) --
// .scratch/fblocks-ui/issues/08-ingest-stalls-after-rtp-packet-loss.md.
// Unlike Close()'s own corruption-retry (which already has the complete
// final content ready to rewrite in one shot at a fresh index), a
// mid-recording failure can't be retried inline here — more frames are
// still expected to arrive for this channel. Marks this fblock Bad, closes
// this segmentImpl and releases its pool slot (so a fresh segment can be
// promoted), and returns ErrSegmentClosed: internal/ingest's
// StorageSegment.call already retries on exactly that error to open a new
// segment via BeginSegment on its next call, so no new retry plumbing is
// needed there.
func (s *segmentImpl) failLocked(now uint64) error {
	u := s.unit
	s.closed = true
	err := u.failFblockWrite(s.idx, s.uuid)
	if err != nil {
		return err
	}
	err = u.pool.release(s, now)
	if err != nil {
		return err
	}
	return ErrSegmentClosed
}

func (s *segmentImpl) RegisterChannel(channel uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSegmentClosed
	}
	return s.registerChannelLocked(channel)
}

func (s *segmentImpl) registerChannelLocked(channel uint16) error {
	if _, ok := s.positions[channel]; ok {
		return nil
	}
	pos, err := s.unit.mgr.RegisterChannel(channel)
	if err != nil {
		return err
	}
	s.positions[channel] = pos
	if s.promoted {
		err = s.unit.mgr.SetChannelBit(s.idx, pos, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *segmentImpl) Elements() []mediatree.Element {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filler.Elements()
}

// Close finalizes the segment. See the Segment interface doc for the
// overall contract; this implementation's happy path finishes the
// incremental open write and writes just the tail (see writeTailLocked);
// on any corruption it falls back to a full plain rewrite at a fresh index
// (see the retry loop below) -- segmentImpl always holds the complete,
// authoritative content in s.filler regardless of what happened to the
// engine job, so the fallback never loses data, it just gives up the
// early-index/periodic-flush benefit for that one retry.
func (s *segmentImpl) Close(now uint64) ([16]byte, error) {
	s.closingFlag.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.uuid, ErrSegmentClosed
	}
	s.closed = true
	return s.closeLocked(now)
}

// closeLocked is Close's actual body, assuming s.mu is already held and
// s.closed has already been set true by the caller -- lets a caller that
// already holds s.mu (AddFrames/AddStreamParams's fullness check) finalize
// the segment without re-entering Close and deadlocking on s.mu.Lock().
func (s *segmentImpl) closeLocked(now uint64) ([16]byte, error) {
	u := s.unit

	if !s.promoted {
		// Never reached the pool's head -- no physical index was ever
		// assigned, nothing was ever written. Not expected in production
		// (Close is pool-internal/fullness-driven and only ever targets
		// whichever segment IS currently promoted), but kept safe.
		return s.uuid, nil
	}

	elems := s.filler.Elements()
	contentBuf := mediatree.EncodeContent(elems)
	// An empty tree (this segment's Close was called -- pool-driven, by
	// fullness -- before any AddStreamParams/AddFrames ever landed on it)
	// has no TOC to build, same as fblock 0's init write: an absent tree,
	// toc_size a true 0, not an error.
	var tocBuf []byte
	if len(elems) > 0 {
		_, valueOffsets, err := mediatree.DecodeContentWithOffsets(contentBuf)
		if err != nil {
			return s.uuid, fmt.Errorf("storage: segment: re-decode content offsets: %w", err)
		}
		columns, err := toc.Build(elems, valueOffsets)
		if err != nil {
			return s.uuid, fmt.Errorf("storage: segment: build TOC: %w", err)
		}
		tocBuf, err = toc.Encode(columns)
		if err != nil {
			return s.uuid, fmt.Errorf("storage: segment: encode TOC: %w", err)
		}
	}

	ticket := s.handle.Close()
	res, err := ticket.Wait()
	if err != nil {
		u.health.RecordWrite(true)
		return s.uuid, fmt.Errorf("storage: segment: finish open write for fblock %d: %w", s.idx, err)
	}
	if !res.Corrupted {
		ok, err := s.writeTailLocked(contentBuf, tocBuf, now)
		if err != nil {
			return s.uuid, err
		}
		if ok {
			return s.uuid, nil
		}
		// The tail write itself corrupted -- fall through to the full
		// rewrite retry below (idx already marked Bad by writeTailLocked).
	} else {
		err = u.failFblockWrite(s.idx, s.uuid)
		if err != nil {
			return s.uuid, err
		}
	}

	for {
		idx, h, err := u.beginFblockWrite(now, s.uuid, s.positions, s.begin, s.end)
		if err != nil {
			return s.uuid, err
		}

		buf, err := assembleFblock(h, contentBuf, tocBuf, u.backend.Alignment())
		if err != nil {
			return s.uuid, fmt.Errorf("storage: segment: assemble fblock %d: %w", idx, err)
		}

		writeTicket := u.engine.EnqueueWrite(int64(fblockOffset(u.geo, idx)), buf)
		res, werr := writeTicket.Wait()
		if werr != nil {
			u.health.RecordWrite(true)
			return s.uuid, fmt.Errorf("storage: segment: write fblock %d: %w", idx, werr)
		}
		if res.Corrupted {
			err = u.failFblockWrite(idx, s.uuid)
			if err != nil {
				return s.uuid, err
			}
			continue
		}

		err = u.completeFblockWrite(idx, s.uuid, s.begin, s.end, h.Prolog.WriteSequence, now)
		if err != nil {
			return s.uuid, err
		}
		s.idx = idx
		s.idxAtomic.Store(idx)
		err = u.pool.release(s, now)
		if err != nil {
			return s.uuid, err
		}
		return s.uuid, nil
	}
}

// writeTailLocked writes the segment's tail — remaining un-flushed content
// (from WriteHandle.Written() onward) zero-padded to capacity, + magic_toc
// + toc + epilog — as one plain EnqueueWrite overwriting the last magic
// trailer, and completes the write on success. Returns ok=false (caller
// falls back to a full rewrite) if this write itself corrupts.
func (s *segmentImpl) writeTailLocked(contentBuf, tocBuf []byte, now uint64) (ok bool, err error) {
	u := s.unit
	written := s.handle.Written()
	contentSoFar := written - s.headerLen
	if contentSoFar < 0 || contentSoFar > int64(len(contentBuf)) {
		return false, fmt.Errorf("storage: segment: inconsistent flush accounting for fblock %d (written=%d headerLen=%d contentLen=%d)", s.idx, written, s.headerLen, len(contentBuf))
	}

	tail, err := assembleTail(contentBuf, tocBuf, u.geo.FblockSize, s.paramsSize, s.catalogSize, u.backend.Alignment(), contentSoFar)
	if err != nil {
		return false, fmt.Errorf("storage: segment: assemble tail for fblock %d: %w", s.idx, err)
	}

	ticket := u.engine.EnqueueWrite(int64(fblockOffset(u.geo, s.idx))+s.handle.TrailerOffset(), tail)
	res, werr := ticket.Wait()
	if werr != nil {
		u.health.RecordWrite(true)
		return false, fmt.Errorf("storage: segment: write tail for fblock %d: %w", s.idx, werr)
	}
	if res.Corrupted {
		err = u.failFblockWrite(s.idx, s.uuid)
		if err != nil {
			return false, err
		}
		return false, nil
	}

	err = u.completeFblockWrite(s.idx, s.uuid, s.begin, s.end, s.writeSeq, now)
	if err != nil {
		return false, err
	}
	err = u.pool.release(s, now)
	if err != nil {
		return false, err
	}
	return true, nil
}
