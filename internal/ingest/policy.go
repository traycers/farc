package ingest

import (
	"errors"
	"fmt"
	"sync"

	"traycers/farc/internal/fcontainer"
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

// Recorder is the subset of internal/storage.Unit CapturePolicy needs. A
// real *storage.Unit satisfies this directly. "Get/return a buffer" per
// docs/docs/archive/00-requirements.md §4.8 has no separate handshake in
// this v1 (ADR-017's streaming write is deferred, see internal/storage's
// package doc) — "get a buffer" is just allocating a fresh
// *fcontainer.Filler locally, and "return it" is this one call.
type Recorder interface {
	WriteFcontainer(channels []uint16, begin, end uint64, filler *fcontainer.Filler, now uint64) ([16]byte, error)
}

// CapturePolicy is one channel's capture strategy plus all the state that
// must survive a §6 SetPolicy swap: the frame queue, cached last-known
// stream params (independent of segment state), and the currently open
// segment (if any). Continuous and event share this one state machine —
// see this package's doc comment — differing only in which admin command
// opens/extends a segment and whether Tick ever does anything.
type CapturePolicy struct {
	mu sync.Mutex

	channel  uint16
	recorder Recorder

	policyType PolicyType
	params     PolicyParams

	queue        *FrameQueue
	cachedParams map[StreamID]*fcontainer.StreamParams

	recording  bool
	stopAt     uint64
	stopAtSet  bool
	filler     *fcontainer.Filler
	configIDs  map[StreamID]uint32
	configVers map[StreamID]*fcontainer.StreamParams // which params version configIDs[id] was built from
	haveFrame  bool
	begin, end uint64

	// onRecordingChange fires on every actual p.recording flip, from
	// whichever admin command or internal state machine (Tick's stop_at
	// expiry, Trigger's auto-start) caused it -- distinct from the admin
	// command itself, which internal/api publishes separately. Defaults to
	// a no-op so existing callers/tests that never call
	// SetOnRecordingChange are unaffected.
	onRecordingChange func(channel uint16, recording bool)
}

// NewCapturePolicy creates a CapturePolicy for channel, initially idle.
// queueDepth (ns) is the frame queue's retention window — a channel-level
// constant, not touched by SetPolicy (docs/docs/archive/
// 10-capture-policy.md §6: "очередь... передаётся как есть").
func NewCapturePolicy(channel uint16, recorder Recorder, queueDepth uint64, policyType PolicyType, params PolicyParams) *CapturePolicy {
	return &CapturePolicy{
		channel:           channel,
		recorder:          recorder,
		policyType:        policyType,
		params:            params,
		queue:             NewFrameQueue(queueDepth),
		cachedParams:      make(map[StreamID]*fcontainer.StreamParams),
		configIDs:         make(map[StreamID]uint32),
		configVers:        make(map[StreamID]*fcontainer.StreamParams),
		onRecordingChange: func(uint16, bool) {},
	}
}

// SetOnRecordingChange installs a hook fired every time p.recording actually
// flips, called with p's own mutex held (so the hook must not call back into
// p). A nil fn resets to a no-op.
func (p *CapturePolicy) SetOnRecordingChange(fn func(channel uint16, recording bool)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fn == nil {
		fn = func(uint16, bool) {}
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

// HandleFrame is called for every decoded frame (§4). It always queues the
// frame; if a segment is open, it's also forwarded to the Filler
// immediately.
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
	cid, err := p.ensureConfigLocked(id, params)
	if err != nil {
		return err
	}
	err = p.filler.AddFrames(cid, []fcontainer.Frame{frame})
	if err != nil {
		return err
	}
	p.trackTimeLocked(frame.Time)
	return nil
}

// ensureConfigLocked returns the current segment's config id for id,
// adding a new config node (a new version) if params differs by identity
// from whatever was last used — pointer identity, not value equality,
// because cachedParams always stores a fresh copy per SetStreamParams
// call, so identity exactly captures "a genuinely new params event", which
// is what should open a new config node.
func (p *CapturePolicy) ensureConfigLocked(id StreamID, params *fcontainer.StreamParams) (uint32, error) {
	if cid, ok := p.configIDs[id]; ok && p.configVers[id] == params {
		return cid, nil
	}
	cid, err := p.filler.AddStreamParams(uint32(p.channel), id.Stream, id.Kind, *params)
	if err != nil {
		return 0, err
	}
	p.configIDs[id] = cid
	p.configVers[id] = params
	return cid, nil
}

func (p *CapturePolicy) trackTimeLocked(t uint64) {
	if !p.haveFrame {
		p.begin, p.end = t, t
		p.haveFrame = true
		return
	}
	if t < p.begin {
		p.begin = t
	}
	if t > p.end {
		p.end = t
	}
}

// openSegmentLocked opens a fresh segment and replays every queued frame
// with time >= replayFrom into it, in order (§5.1/§5.2), adding whichever
// distinct config versions they span along the way.
func (p *CapturePolicy) openSegmentLocked(replayFrom uint64) error {
	p.filler = fcontainer.New()
	p.configIDs = make(map[StreamID]uint32)
	p.configVers = make(map[StreamID]*fcontainer.StreamParams)
	p.haveFrame = false
	p.recording = true
	p.onRecordingChange(p.channel, true)

	for _, qf := range p.queue.Since(replayFrom) {
		id := StreamID{qf.Stream, qf.Kind}
		cid, err := p.ensureConfigLocked(id, qf.Params)
		if err != nil {
			return fmt.Errorf("ingest: replay: %w", err)
		}
		err = p.filler.AddFrames(cid, []fcontainer.Frame{qf.Frame})
		if err != nil {
			return fmt.Errorf("ingest: replay: %w", err)
		}
		p.trackTimeLocked(qf.Frame.Time)
	}
	return nil
}

// closeSegmentLocked closes the current segment (if any) and hands the
// finished fcontainer to Recorder. A segment that never received a single
// frame is simply discarded — the docs don't cover this case explicitly,
// and writing an empty fcontainer would serve no purpose.
func (p *CapturePolicy) closeSegmentLocked(now uint64) error {
	if !p.recording {
		return nil
	}
	p.recording = false
	p.onRecordingChange(p.channel, false)
	p.stopAtSet = false
	filler := p.filler
	begin, end, wrote := p.begin, p.end, p.haveFrame
	p.filler = nil
	p.haveFrame = false
	if !wrote {
		return nil
	}
	_, err := p.recorder.WriteFcontainer([]uint16{p.channel}, begin, end, filler, now)
	return err
}

// StartRecording is continuous's "начать запись [with from_time]" (§5.1).
// A nil fromTime means "from_time = now" (no queue replay). Idempotent if
// already recording.
func (p *CapturePolicy) StartRecording(now uint64, fromTime *uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.policyType != PolicyContinuous {
		return ErrWrongPolicyType
	}
	if p.recording {
		return nil
	}
	replay := now
	if fromTime != nil {
		replay = *fromTime
	}
	return p.openSegmentLocked(replay)
}

// StopRecording is continuous's "остановить запись" (§5.1).
func (p *CapturePolicy) StopRecording(now uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.policyType != PolicyContinuous {
		return ErrWrongPolicyType
	}
	return p.closeSegmentLocked(now)
}

// Trigger is event's incoming event(t) (§5.2): opens a segment (replaying
// [eventTime-prerecord, eventTime]) if idle, or extends stop_at
// (never shrinking it) if already recording.
func (p *CapturePolicy) Trigger(now, eventTime uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.policyType != PolicyEvent {
		return ErrWrongPolicyType
	}

	candidate := eventTime + p.params.Postrecord
	if !p.recording {
		err := p.openSegmentLocked(saturatingSub(eventTime, p.params.Prerecord))
		if err != nil {
			return err
		}
		p.stopAt, p.stopAtSet = candidate, true
		return nil
	}
	if !p.stopAtSet || candidate > p.stopAt {
		p.stopAt, p.stopAtSet = candidate, true
	}
	return nil
}

// Tick lets event's stop_at expiry fire without waiting for a new frame or
// event (§5.2: "требует таймера/периодической проверки"). No-op for
// continuous, which never sets stop_at.
func (p *CapturePolicy) Tick(now uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.policyType != PolicyEvent {
		return nil
	}
	if p.recording && p.stopAtSet && now >= p.stopAt {
		return p.closeSegmentLocked(now)
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

// Close forces the current segment closed, if any, regardless of policy
// type — used when ChannelIngest itself is shutting down.
func (p *CapturePolicy) Close(now uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeSegmentLocked(now)
}

func saturatingSub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}
