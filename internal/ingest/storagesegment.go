package ingest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
)

// SegmentBackend is the subset of *storage.Unit CapturePolicy's shared
// segment needs. A real *storage.Unit satisfies this directly (renamed
// from this package's old Recorder -- WriteFcontainer's whole-fcontainer
// shape no longer fits what ingest calls; internal/storage's segment.go
// builds one shared segment across every channel of a Storage instead).
type SegmentBackend interface {
	BeginSegment(channels []uint16, now uint64) (storage.Segment, storage.PoolStatus, int64, error)
}

// StorageSegment is one Storage's currently-active shared segment,
// addressed by every CapturePolicy of that Storage's channels
// (docs/docs/archive/00-requirements.md: "один фконтейнер может содержать
// данные нескольких каналов одновременно... обычный режим"). mu exists
// primarily because *which* storage.Segment is "current" can change out
// from under a caller (the pool rotates purely by fullness, independent of
// any one channel) and because "is my channel already joined on this
// generation" must be checked-and-set atomically with the write itself --
// not because storage.Segment isn't itself already concurrency-safe (its
// own Filler already serializes AddFrames/AddStreamParams internally).
type StorageSegment struct {
	mu      sync.Mutex
	backend SegmentBackend
	current storage.Segment
	gen     uint64          // bumps every time `current` is replaced
	joined  map[uint16]bool // channels already RegisterChannel'd on `current`
}

func newStorageSegment(backend SegmentBackend) *StorageSegment {
	return &StorageSegment{backend: backend, joined: make(map[uint16]bool)}
}

// ensureLocked returns the active underlying segment, calling
// backend.BeginSegment(channels: {channel}) if none is active. now is Unix
// ns, needed only if this call actually opens a fresh segment -- it comes
// from whatever CapturePolicy call site triggered this (a live frame's own
// timestamp, a stream-params event's timestamp, or an admin command's
// explicit now -- all Unix ns in this codebase, and for a live RTSP source
// close enough to true wall-clock time for retention-check purposes; see
// this method's callers). Only the channel forcing the (re)open is passed
// -- every other channel already sharing this Storage joins on its own
// next call, via joinLocked, not through this argument.
func (s *StorageSegment) ensureLocked(channel uint16, now uint64) (storage.Segment, error) {
	if s.current != nil {
		return s.current, nil
	}
	seg, _, _, err := s.backend.BeginSegment([]uint16{channel}, now)
	if err != nil {
		return nil, err
	}
	s.current, s.gen, s.joined = seg, s.gen+1, map[uint16]bool{channel: true}
	return seg, nil
}

func (s *StorageSegment) joinLocked(channel uint16) error {
	if s.joined[channel] {
		return nil
	}
	err := s.current.RegisterChannel(channel)
	if err != nil {
		return err
	}
	s.joined[channel] = true
	return nil
}

// call runs fn against the current segment, retrying once against a
// freshly (re)opened one if fn reports storage.ErrSegmentClosed -- the
// pool rotated this Storage's segment purely by fullness since the last
// call, with no proactive notification to internal/ingest (matches
// internal/storage's own design: nothing in this package ever calls
// storage.Segment.Close -- that's pool-internal).
func (s *StorageSegment) call(channel uint16, now uint64, fn func(storage.Segment) (uint32, error)) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		seg, err := s.ensureLocked(channel, now)
		if err != nil {
			return 0, err
		}
		err = s.joinLocked(channel)
		if err != nil {
			return 0, err
		}
		cid, err := fn(seg)
		if errors.Is(err, storage.ErrSegmentClosed) {
			s.current = nil
			continue
		}
		return cid, err
	}
	return 0, fmt.Errorf("ingest: storage segment: repeated ErrSegmentClosed reopening for channel %d", channel)
}

// AddStreamParams mirrors storage.Segment.AddStreamParams, transparently
// (re)opening/rejoining the shared segment as needed.
func (s *StorageSegment) AddStreamParams(channel uint16, now uint64, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (uint32, error) {
	return s.call(channel, now, func(seg storage.Segment) (uint32, error) {
		return seg.AddStreamParams(uint32(channel), stream, kind, params)
	})
}

// AddFrames mirrors storage.Segment.AddFrames, transparently
// (re)opening/rejoining the shared segment as needed.
func (s *StorageSegment) AddFrames(channel uint16, now uint64, configID uint32, frames []fcontainer.Frame) error {
	_, err := s.call(channel, now, func(seg storage.Segment) (uint32, error) {
		return 0, seg.AddFrames(configID, frames)
	})
	return err
}

// Generation lets a CapturePolicy detect "the shared segment changed since
// I last wrote" -- configID cache invalidation, and LiveSnapshot's own
// "new segment, not just more frames" signal.
func (s *StorageSegment) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// Elements returns the current segment's whole tree so far -- every
// channel sharing it, merged (LiveSnapshot filters this down to one
// channel's own subtree, see livefilter.go). Nil if no segment is
// currently open (nothing has ever been written, or the pool rotated and
// nothing has re-opened it yet).
func (s *StorageSegment) Elements() []mediatree.Element {
	s.mu.Lock()
	seg := s.current
	s.mu.Unlock()
	if seg == nil {
		return nil
	}
	return seg.Elements()
}
