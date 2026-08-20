package ingest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// PolicyType selects a CapturePolicy strategy (docs/docs/archive/
// 10-capture-policy.md §5). PolicySchedule is intentionally absent — see
// this package's doc comment.
type PolicyType int

const (
	PolicyContinuous PolicyType = iota
	PolicyEvent
)

func (t PolicyType) String() string {
	if t == PolicyEvent {
		return "event"
	}
	return "continuous"
}

// PolicyParams are the strategy-specific tunables — meaningful only for
// PolicyEvent (§5.2); ignored for PolicyContinuous.
type PolicyParams struct {
	Prerecord  uint64 // ns
	Postrecord uint64 // ns
}

// ErrWrongPolicyType is returned by an administrative command not valid
// for the CapturePolicy's current type (e.g. Trigger on a continuous
// policy, or StartRecording on an event one).
var ErrWrongPolicyType = errors.New("ingest: command not valid for this CapturePolicy's current type")

// CapturePolicy is one channel's capture strategy plus all the state that
// must survive a §6 SetPolicy swap: the frame queue, cached last-known
// stream params (independent of segment state), and this channel's own
// recording state. Continuous and event share this one state machine —
// see this package's doc comment — differing only in which admin command
// opens/extends a segment and whether Tick ever does anything.
//
// Unlike before this ticket, CapturePolicy no longer owns a private
// *fcontainer.Filler -- every channel of a Storage now shares one
// *StorageSegment (segment), addressing whichever underlying
// storage.Segment the pool currently has open for that Storage
// (docs/docs/archive/00-requirements.md: "постоянная запись всех каналов
// сразу — обычный режим"). Closing is therefore no longer something a
// channel's own stop *triggers* -- it's purely fullness-driven, one layer
// down in internal/storage's buffer pool -- so closeSegmentLocked only
// ever stops this channel's own contribution, never the shared segment.
type CapturePolicy struct {
	mu sync.Mutex

	channel uint16
	segment *StorageSegment

	policyType PolicyType
	params     PolicyParams

	queue        *FrameQueue
	cachedParams map[StreamID]*fcontainer.StreamParams

	recording  bool
	stopAt     uint64
	stopAtSet  bool
	configIDs  map[StreamID]uint32
	configVers map[StreamID]*fcontainer.StreamParams // which params version configIDs[id] was built from

	// sharedGen is the segment.Generation() last observed by this policy --
	// a mismatch (checked in ensureConfigLocked) means the shared segment
	// rotated (pool-driven, by fullness) since this channel last wrote,
	// independent of anything this channel itself did; configIDs/configVers
	// are stale in that case (they name nodes in a segment that's gone) and
	// must be rebuilt against the new one.
	sharedGen uint64

	// generation increments every openSegmentLocked call (and, via
	// ensureConfigLocked, on any mid-recording rotation it detects) --
	// LiveSnapshot's unambiguous "a new segment replaced the old one"
	// signal for a poller (e.g. internal/api's fblock-live WS handler).
	// Comparing element counts alone can't distinguish "many frames arrived
	// since the last poll" from "a new, currently-smaller segment started";
	// misreading the latter as the former would attach new nodes onto the
	// old tree using stale ids.
	generation uint64

	// onRecordingChange fires on every actual p.recording flip, from
	// whichever admin command or internal state machine (Tick's stop_at
	// expiry, Trigger's auto-start) caused it -- distinct from the admin
	// command itself, which internal/api publishes separately. Defaults to
	// a no-op so existing callers/tests that never call
	// SetOnRecordingChange are unaffected. t is the segment's intended
	// begin time on a start (openSegmentLocked's replayFrom -- the earliest
	// time this segment's content is meant to cover, including any
	// prerecord/queue replay) or its actual stop time on a stop
	// (closeSegmentLocked's now) -- not derived from the frames actually
	// written.
	onRecordingChange func(channel uint16, recording bool, t uint64)
}

// NewCapturePolicy creates a CapturePolicy for channel, initially idle,
// writing into segment (shared with every other channel of the same
// Storage). queueDepth (ns) is the frame queue's retention window — a
// channel-level constant, not touched by SetPolicy (docs/docs/archive/
// 10-capture-policy.md §6: "очередь... передаётся как есть").
func NewCapturePolicy(channel uint16, segment *StorageSegment, queueDepth uint64, policyType PolicyType, params PolicyParams) *CapturePolicy {
	return &CapturePolicy{
		channel:           channel,
		segment:           segment,
		policyType:        policyType,
		params:            params,
		queue:             NewFrameQueue(queueDepth),
		cachedParams:      make(map[StreamID]*fcontainer.StreamParams),
		configIDs:         make(map[StreamID]uint32),
		configVers:        make(map[StreamID]*fcontainer.StreamParams),
		onRecordingChange: func(uint16, bool, uint64) {},
	}
}

// SetOnRecordingChange installs a hook fired every time p.recording actually
// flips, called after p's own mutex is released -- safe to call back into
// any of p's other methods from within the hook. A nil fn resets to a
// no-op.
func (p *CapturePolicy) SetOnRecordingChange(fn func(channel uint16, recording bool, t uint64)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fn == nil {
		fn = func(uint16, bool, uint64) {}
	}
	p.onRecordingChange = fn
}

// SetStreamParams records sp as the current codec parameters for
// (stream, kind), independent of whether a segment is open (§3: "кэширует
// последние известные параметры потока — независимо от состояния
// сегмента"). It does not itself touch the Filler even if recording —
// HandleFrame's ensureConfigLocked adds the new config lazily, the first
// time a frame under it actually arrives (§3's "сначала параметры, потом
// кадры" is satisfied because a config id only ever comes from this same
// params value).
func (p *CapturePolicy) SetStreamParams(stream uint32, kind fcontainer.StreamKind, sp fcontainer.StreamParams) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cachedParams[StreamID{stream, kind}] = &sp
}

// CachedParams returns a copy of (stream, kind)'s current cached params, so
// a caller (internal/ingest/rtsp.go, at RTSP setup/reconnect) can compare
// freshly negotiated params against what's already active before deciding
// whether a genuinely new SetStreamParams call is warranted -- this method
// itself makes no such decision, it's a plain read of the cache
// SetStreamParams writes.
func (p *CapturePolicy) CachedParams(stream uint32, kind fcontainer.StreamKind) (fcontainer.StreamParams, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sp, ok := p.cachedParams[StreamID{stream, kind}]
	if !ok {
		return fcontainer.StreamParams{}, false
	}
	return *sp, true
}

// HandleFrame is called for every decoded frame (§4). It always queues the
// frame; if recording, it's also forwarded to the shared segment
// immediately. frame.Time doubles as this call's "now" for the shared
// segment's own bookkeeping (a fresh/rotated segment may need to open,
// which needs a wall-clock-ish timestamp) -- close enough for a live RTSP
// source, where frame timestamps track real time.
func (p *CapturePolicy) HandleFrame(stream uint32, kind fcontainer.StreamKind, frame fcontainer.Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := StreamID{stream, kind}
	params, ok := p.cachedParams[id]
	if !ok {
		return fmt.Errorf("ingest: frame for stream %d/%v arrived before any SetStreamParams call", stream, kind)
	}
	p.queue.Push(frame.Time, QueuedFrame{Stream: stream, Kind: kind, Frame: frame, Params: params})

	if !p.recording {
		return nil
	}
	return p.addFrameLocked(id, params, frame)
}

// ensureConfigLocked returns the shared segment's config id for id, adding
// a new config node (a new version) if params differs by identity from
// whatever was last used against the *current* segment generation —
// pointer identity, not value equality, because cachedParams always stores
// a fresh copy per SetStreamParams call, so identity exactly captures "a
// genuinely new params event", which is what should open a new config
// node. A segment.Generation() mismatch against p.sharedGen means the
// shared segment rotated (pool-driven, by fullness) since this channel
// last wrote — independent of anything this channel did — so every cached
// configID is stale (they'd name nodes in a segment that's gone) and must
// be dropped; this is also LiveSnapshot's "new segment" signal, so
// p.generation bumps here too, same as an explicit open.
//
// p.sharedGen is re-synced after a successful AddStreamParams below, not
// just at entry: the very first write through a freshly-opened
// StorageSegment lazily opens the underlying segment as a side effect
// (StorageSegment.ensureLocked), bumping Generation() from 0 to 1 *during*
// this call -- if p.sharedGen were left at its pre-call value, the very
// next call would see that bump as a "the shared segment rotated
// externally" mismatch that never actually happened, wiping configIDs/
// configVers and spuriously reopening a config for unchanged params.
//
// This resync trades away this method's ability to *itself* always catch a
// rotation that happens between two calls with no intervening write for
// this id -- Generation() only discovers such a rotation reactively, when
// something actually tries to write and hits storage.ErrSegmentClosed, so
// a pure pre-write comparison can never proactively see it. addFrameLocked
// is where that's actually caught: it retries once against a fresh config
// if the write after this call fails with fcontainer.ErrStaleConfigID.
//
// When a new config node is actually opened, its Time is stamped with now
// (the very frame that triggered this call) rather than whatever
// params.Time happened to hold — params comes from rtsp.go, which builds
// StreamParams purely from RTSP/SDP negotiation and has no reason to know
// a meaningful "moment of change" itself; now is that moment (the first
// frame recorded under these params, per 07-media-tree.md §5's "config's
// identity is a moment in time"). params itself (the cached pointer) is
// left untouched so identity comparisons elsewhere are unaffected.
func (p *CapturePolicy) ensureConfigLocked(id StreamID, params *fcontainer.StreamParams, now uint64) (uint32, error) {
	if gen := p.segment.Generation(); gen != p.sharedGen {
		p.configIDs = make(map[StreamID]uint32)
		p.configVers = make(map[StreamID]*fcontainer.StreamParams)
		p.sharedGen = gen
		p.generation++
	}
	if cid, ok := p.configIDs[id]; ok && p.configVers[id] == params {
		return cid, nil
	}
	stamped := *params
	stamped.Time = now
	cid, err := p.segment.AddStreamParams(p.channel, now, id.Stream, id.Kind, stamped)
	if err != nil {
		return 0, err
	}
	p.sharedGen = p.segment.Generation()
	p.configIDs[id] = cid
	p.configVers[id] = params
	return cid, nil
}

// addFrameLocked mints (or reuses, via ensureConfigLocked) a config for id
// and writes frame through it, retrying once against a freshly-minted
// config if the shared segment rotated out from under the reused cid
// between ensureConfigLocked's own (necessarily lazy, see its doc comment)
// generation check and this write -- the one case ensureConfigLocked
// cannot itself detect. Shared by HandleFrame and openSegmentLocked's
// replay loop, both of which need the same recovery.
func (p *CapturePolicy) addFrameLocked(id StreamID, params *fcontainer.StreamParams, frame fcontainer.Frame) error {
	cid, err := p.ensureConfigLocked(id, params, frame.Time)
	if err != nil {
		return err
	}
	err = p.segment.AddFrames(p.channel, frame.Time, cid, []fcontainer.Frame{frame})
	if !errors.Is(err, fcontainer.ErrStaleConfigID) {
		return err
	}
	p.configIDs = make(map[StreamID]uint32)
	p.configVers = make(map[StreamID]*fcontainer.StreamParams)
	p.sharedGen = p.segment.Generation()
	p.generation++ // LiveSnapshot's "new segment" signal -- same bump ensureConfigLocked's own mismatch branch gives a rotation caught there
	cid, err = p.ensureConfigLocked(id, params, frame.Time)
	if err != nil {
		return err
	}
	return p.segment.AddFrames(p.channel, frame.Time, cid, []fcontainer.Frame{frame})
}

// openSegmentLocked marks this channel recording and replays every queued
// frame with time >= replayFrom into the shared segment, in order
// (§5.1/§5.2), adding whichever distinct config versions they span along
// the way. It does not create anything of its own — "opening" a segment is
// now purely a StorageSegment/pool-level event (lazy, on first write);
// this only marks the *channel's own* intent to contribute from here on.
// Does not itself fire onRecordingChange -- every caller always flips
// p.recording false->true by calling this, so it fires the hook itself,
// once, after releasing p.mu (see StartRecording/Trigger).
func (p *CapturePolicy) openSegmentLocked(replayFrom uint64) error {
	p.generation++
	p.configIDs = make(map[StreamID]uint32)
	p.configVers = make(map[StreamID]*fcontainer.StreamParams)
	p.sharedGen = p.segment.Generation()
	p.recording = true

	for _, qf := range p.queue.Since(replayFrom) {
		id := StreamID{qf.Stream, qf.Kind}
		err := p.addFrameLocked(id, qf.Params, qf.Frame)
		if err != nil {
			return fmt.Errorf("ingest: replay: %w", err)
		}
	}
	return nil
}

// closeSegmentLocked stops this channel from contributing further frames.
// It does NOT finalize/close the shared segment — that's purely
// fullness-driven, one layer down in internal/storage's buffer pool
// (docs/docs/archive/00-requirements.md's "Close dynamics": a channel
// stopping must not affect any other channel still sharing the same
// segment). From this channel's own perspective, a segment ending is
// something that *happens to* it (its next write transparently reopens
// against a fresh one, see StorageSegment.call) rather than something a
// stop *triggers*.
//
// Returns whether this call actually flipped p.recording (false if already
// idle, a no-op) -- callers use this to decide whether to fire
// onRecordingChange themselves, after releasing p.mu.
func (p *CapturePolicy) closeSegmentLocked() bool {
	if !p.recording {
		return false
	}
	p.recording = false
	p.stopAtSet = false
	return true
}

// LiveSnapshot is CapturePolicy's thread-safe read for a poller that wants
// to observe this channel's own in-progress segment tree as it grows (e.g.
// internal/api's fblock-live WS handler) -- Elements is nil when not
// recording. The shared segment's Elements() merges every channel writing
// to it; filterChannelElements (livefilter.go) extracts just this
// channel's own subtree so LiveSnapshot's per-channel contract stays
// exactly what it was before this ticket.
type LiveSnapshot struct {
	Elements   []mediatree.Element
	Recording  bool
	Generation uint64
}

// LiveSnapshot returns this channel's own subtree of the shared segment so
// far, Generation, and recording state.
func (p *CapturePolicy) LiveSnapshot() LiveSnapshot {
	p.mu.Lock()
	recording, gen, channel, segment := p.recording, p.generation, p.channel, p.segment
	p.mu.Unlock()
	if !recording {
		return LiveSnapshot{Recording: recording, Generation: gen}
	}
	return LiveSnapshot{Elements: filterChannelElements(segment.Elements(), uint32(channel)), Recording: recording, Generation: gen}
}

// StartRecording is continuous's "начать запись [with from_time]" (§5.1).
// A nil fromTime means "from_time = now" (no queue replay). Idempotent if
// already recording.
//
// onRecordingChange fires after p.mu is released (not from inside
// openSegmentLocked), unconditionally once the flip happens -- even if
// openSegmentLocked's own replay then fails -- so the hook may safely call
// back into any other p.mu-guarded method (e.g. Policy) without
// deadlocking on this same, non-reentrant mutex.
func (p *CapturePolicy) StartRecording(now uint64, fromTime *uint64) error {
	p.mu.Lock()
	if p.policyType != PolicyContinuous {
		p.mu.Unlock()
		return ErrWrongPolicyType
	}
	if p.recording {
		p.mu.Unlock()
		return nil
	}
	replay := now
	if fromTime != nil {
		replay = *fromTime
	}
	err := p.openSegmentLocked(replay)
	hook, channel := p.onRecordingChange, p.channel
	p.mu.Unlock()
	hook(channel, true, replay)
	return err
}

// StopRecording is continuous's "остановить запись" (§5.1). See
// StartRecording's doc comment on onRecordingChange's lock-free firing.
func (p *CapturePolicy) StopRecording(now uint64) error {
	p.mu.Lock()
	if p.policyType != PolicyContinuous {
		p.mu.Unlock()
		return ErrWrongPolicyType
	}
	fired := p.closeSegmentLocked()
	hook, channel := p.onRecordingChange, p.channel
	p.mu.Unlock()
	if fired {
		hook(channel, false, now)
	}
	return nil
}

// Trigger is event's incoming event(t) (§5.2): opens a segment (replaying
// [eventTime-prerecord, eventTime]) if idle, or extends stop_at
// (never shrinking it) if already recording. See StartRecording's doc
// comment on onRecordingChange's lock-free firing.
func (p *CapturePolicy) Trigger(now, eventTime uint64) error {
	p.mu.Lock()
	if p.policyType != PolicyEvent {
		p.mu.Unlock()
		return ErrWrongPolicyType
	}

	candidate := eventTime + p.params.Postrecord
	var err error
	fired, replay := false, uint64(0)
	if !p.recording {
		replay = saturatingSub(eventTime, p.params.Prerecord)
		err = p.openSegmentLocked(replay)
		fired = true
		if err == nil {
			p.stopAt, p.stopAtSet = candidate, true
		}
	} else if !p.stopAtSet || candidate > p.stopAt {
		p.stopAt, p.stopAtSet = candidate, true
	}
	hook, channel := p.onRecordingChange, p.channel
	p.mu.Unlock()
	if fired {
		hook(channel, true, replay)
	}
	return err
}

// Tick lets event's stop_at expiry fire without waiting for a new frame or
// event (§5.2: "требует таймера/периодической проверки"). No-op for
// continuous, which never sets stop_at. See StartRecording's doc comment
// on onRecordingChange's lock-free firing.
func (p *CapturePolicy) Tick(now uint64) error {
	p.mu.Lock()
	if p.policyType != PolicyEvent {
		p.mu.Unlock()
		return nil
	}
	fired := false
	if p.recording && p.stopAtSet && now >= p.stopAt {
		fired = p.closeSegmentLocked()
	}
	hook, channel := p.onRecordingChange, p.channel
	p.mu.Unlock()
	if fired {
		hook(channel, false, now)
	}
	return nil
}

// SetPolicy swaps the active strategy in place (§6), keeping the queue,
// cached params, and any open segment untouched — the segment immediately
// falls under the new strategy's rules. Switching to PolicyContinuous
// clears stop_at (continuous never uses it); switching away from it
// leaves stop_at unset until the first Trigger, matching §6's "не
// назначается искусственно".
func (p *CapturePolicy) SetPolicy(policyType PolicyType, params PolicyParams) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policyType = policyType
	p.params = params
	if policyType == PolicyContinuous {
		p.stopAtSet = false
	}
}

// Policy returns the currently active strategy and its params (§6's
// SetPolicy target) -- read-only, for reporting (GET /channels).
func (p *CapturePolicy) Policy() (PolicyType, PolicyParams) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.policyType, p.params
}

// Recording reports whether this channel is currently recording -- a cheap
// read-only accessor for GET /channels' status (mirrors ChannelIngest.
// Connected()), unlike LiveSnapshot which also builds this channel's
// Elements subtree, unnecessary work for just a status flag.
func (p *CapturePolicy) Recording() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.recording
}

// Close forces the current segment closed, if any, regardless of policy
// type — used when ChannelIngest itself is shutting down.
func (p *CapturePolicy) Close(now uint64) error {
	p.mu.Lock()
	fired := p.closeSegmentLocked()
	hook, channel := p.onRecordingChange, p.channel
	p.mu.Unlock()
	if fired {
		hook(channel, false, now)
	}
	return nil
}

func saturatingSub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}
