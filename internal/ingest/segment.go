package ingest

import (
	"errors"
	"sync"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

// errStaleGeneration is returned by sharedSegment.addFrames when gen no
// longer matches the segment's current generation: the caller's cached
// configID was returned by a *fcontainer.Filler instance that has since
// been flushed to disk and discarded. The caller must call addStreamParams
// again (which atomically hands back a fresh configID and the new
// generation) and retry.
var errStaleGeneration = errors.New("ingest: stale segment generation")

// sharedSegment coordinates one storage's currently-open fcontainer, shared
// by every channel currently recording into it -- docs/docs/archive/
// adr/014-channel-registry.md is explicit that this is the real operating
// mode, not an edge case: "один фконтейнер может содержать данные
// нескольких каналов одновременно... вплоть до постоянной записи всех
// каналов сразу". Each CapturePolicy still independently decides WHAT it
// captures and WHEN it personally attaches/detaches (continuous/event,
// prerecord/postrecord -- none of that changes); sharedSegment only decides
// when the content accumulated so far actually gets flushed to disk, since
// with multiple channels sharing one Filler no single channel's own stop/
// event trigger can be allowed to cut off another channel's still-active
// data.
//
// Flush triggers, in order of how they fire:
//   - size: checked after every addFrames call, once the shared Filler's
//     content reaches flushTargetBytes (~min_container_share × fblock_size,
//     resolved by internal/farcd from the storage's own Geometry/Params —
//     see ChannelConfig.SegmentFlushBytes). Storage-level, not tied to any
//     one channel's policy.
//   - last-detach: when the last actively-recording channel detaches,
//     flushed immediately rather than waiting on the size trigger — keeps
//     today's "stop recording -> written promptly" behavior for the common
//     single-channel-per-storage case.
type sharedSegment struct {
	mu sync.Mutex

	recorder         Recorder
	flushTargetBytes int

	filler       *fcontainer.Filler
	generation   uint64
	active       map[uint16]bool
	contributors map[uint16]bool
	segBegin     uint64
	segEnd       uint64
	haveContent  bool
}

// newSharedSegment creates an idle sharedSegment for one storage.
// flushTargetBytes is the content-size threshold that triggers an automatic
// flush (see the type doc's "size" trigger); recorder is the same Recorder
// interface CapturePolicy already depends on.
func newSharedSegment(recorder Recorder, flushTargetBytes int) *sharedSegment {
	return &sharedSegment{
		recorder:         recorder,
		flushTargetBytes: flushTargetBytes,
		active:           make(map[uint16]bool),
		contributors:     make(map[uint16]bool),
	}
}

// attach marks channel as actively contributing to the current fcontainer,
// creating a fresh Filler lazily if none is currently open.
func (s *sharedSegment) attach(channel uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[channel] = true
	if s.filler == nil {
		s.filler = fcontainer.New()
	}
}

// addStreamParams delegates to the current Filler's AddStreamParams,
// returning the resulting configID together with the generation it was
// created under. The caller must pass both back into addFrames unchanged —
// gen is how addFrames detects that a flush happened in between and the
// configID no longer refers to anything in the (new) current Filler.
func (s *sharedSegment) addStreamParams(channel uint16, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (cid uint32, gen uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filler == nil {
		s.filler = fcontainer.New()
	}
	cid, err = s.filler.AddStreamParams(uint32(channel), stream, kind, params)
	if err != nil {
		return 0, 0, err
	}
	s.contributors[channel] = true
	return cid, s.generation, nil
}

// addFrames appends frames under configID, which must have been returned
// together with gen by an addStreamParams call against the segment's
// current generation. Returns errStaleGeneration without touching the
// Filler if a flush happened since — the caller must call addStreamParams
// again and retry rather than risk configID colliding with an unrelated
// node in whatever Filler instance replaced the one it was issued against.
//
// May itself trigger (and return the error of) a size-triggered flush —
// see the type doc.
func (s *sharedSegment) addFrames(gen uint64, channel uint16, configID uint32, frames []fcontainer.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.generation {
		return errStaleGeneration
	}
	err := s.filler.AddFrames(configID, frames)
	if err != nil {
		return err
	}
	s.contributors[channel] = true
	var lastTime uint64
	for _, fr := range frames {
		s.extendRangeLocked(fr.Time)
		lastTime = fr.Time
	}
	if s.flushTargetBytes > 0 && s.filler.ContentBytes() >= s.flushTargetBytes {
		return s.flushLocked(lastTime)
	}
	return nil
}

func (s *sharedSegment) extendRangeLocked(t uint64) {
	if !s.haveContent {
		s.segBegin, s.segEnd, s.haveContent = t, t, true
		return
	}
	if t < s.segBegin {
		s.segBegin = t
	}
	if t > s.segEnd {
		s.segEnd = t
	}
}

// detach marks channel no longer actively recording. If channel was the
// last active one, the accumulated fcontainer (if any) is flushed
// immediately — see the type doc's "last-detach" trigger.
func (s *sharedSegment) detach(channel uint16, now uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, channel)
	if len(s.active) > 0 {
		return nil
	}
	return s.flushLocked(now)
}

// flushLocked writes the accumulated fcontainer, if it actually has any
// content, and resets state for a fresh one — including bumping generation,
// which invalidates every attached CapturePolicy's cached configIDs (they
// referred to the Filler instance just discarded here). A segment that
// never received a single frame is simply discarded, matching
// CapturePolicy.closeSegmentLocked's pre-existing behavior for the
// single-channel case. Must be called with s.mu held.
func (s *sharedSegment) flushLocked(now uint64) error {
	filler := s.filler
	haveContent := s.haveContent
	channels := make([]uint16, 0, len(s.contributors))
	for c := range s.contributors {
		channels = append(channels, c)
	}
	begin, end := s.segBegin, s.segEnd

	s.filler = nil
	s.contributors = make(map[uint16]bool)
	s.haveContent = false
	s.generation++

	if !haveContent {
		return nil
	}
	_, err := s.recorder.WriteFcontainer(channels, begin, end, filler, now)
	return err
}

// liveElementsSince mirrors fcontainer.Filler.ElementsSince/ContentBytes for
// the fblock-live page's per-storage live view. ok is false when no channel
// of this storage is currently recording (no Filler open at all).
func (s *sharedSegment) liveElementsSince(n int) (elems []mediatree.Element, total int, contentBytes int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filler == nil {
		return nil, 0, 0, false
	}
	return s.filler.ElementsSince(n), s.filler.Len(), s.filler.ContentBytes(), true
}
