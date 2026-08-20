package ingest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/mediatree"
)

// ChannelConfig is one configured channel's ingest setup (docs/docs/archive/
// 04-storage-operations.md §2.1's channel list + 10-capture-policy.md §7's
// capture-policy fields). Loading/validating this from the process's JSON
// config file is internal/config's job (Phase 11), not this package's --
// IngestManager just needs the resolved values.
type ChannelConfig struct {
	Channel uint16
	RTSPURL string
	// StorageID is reporting-only (List/GET /channels) -- IngestManager has
	// no Storage awareness at all (see BackpressureSignal's doc below), it
	// just carries the id the caller resolved Recorder from.
	StorageID      string
	SegmentBackend SegmentBackend
	QueueDepth     uint64 // ns
	PolicyType     PolicyType
	PolicyParams   PolicyParams
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	// Name is an optional human-readable label -- reporting-only, same
	// status as StorageID above; IngestManager never keys off it.
	Name string

	// BackpressureSignal, if non-nil, is wired onto the ChannelIngest as
	// its skip-frames check (docs/docs/archive/10-capture-policy.md §8) --
	// see ChannelIngest.SetBackpressureSignal. internal/farcd supplies a
	// closure over the channel's target Storage's StorageEngine.Level();
	// IngestManager itself has no Storage awareness at all, so it just
	// forwards whatever it's given.
	BackpressureSignal func() bool
}

type channelEntry struct {
	cfg    ChannelConfig
	ingest *ChannelIngest
	cancel context.CancelFunc
	done   chan struct{}
}

// ChannelInfo is IngestManager's public listing shape for GET /channels --
// mirrors StorageRegistry.List/StorageInfo's role for storages: the running
// manager is the source of truth for what's actually active, independent
// of whatever config file it may or may not have been built from.
type ChannelInfo struct {
	Channel      uint16
	RTSPURL      string
	StorageID    string
	PolicyType   PolicyType
	PolicyParams PolicyParams
	Name         string
	// Connected is whether this channel's RTSP session is currently
	// established (ChannelIngest.Connected) -- lets a freshly-loaded
	// GET /channels caller see current connectivity, not just the
	// channel.rtsp.connected/disconnected WS events for past transitions.
	Connected bool
	// Recording is whether this channel's CapturePolicy is currently
	// recording (CapturePolicy.Recording()) -- lets a GET /channels caller
	// show live capture status without listening on the
	// channel.recording.started/stopped WS events.
	Recording bool
	// LastConnectError is ChannelIngest.LastConnectError() -- the most
	// recent failed attempt's reason while this channel has never yet
	// connected, empty otherwise. Lets a GET /channels caller see a
	// persistent connect-failure status without having been listening on
	// channel.rtsp.connect_failed at the moment it happened.
	LastConnectError string
}

// IngestManager creates and owns one ChannelIngest per configured channel
// (docs/docs/archive/11-service-composition.md §5.1.1).
type IngestManager struct {
	mu       sync.Mutex
	channels map[uint16]*channelEntry
	// storageSegments is one *StorageSegment per StorageID actually in use
	// -- lazily created on first channel for that Storage (segmentForLocked),
	// shared by every channel of that Storage from then on (the ticket's
	// settled grouping: all channels of a Storage, always). No explicit
	// teardown on RemoveChannel: a channel re-added to the same Storage
	// must rejoin the same still-warm segment, and the real heavy state
	// lives in internal/storage's own pool regardless -- a stale, near-
	// empty entry costs nothing material. Entries live for this
	// IngestManager's own lifetime.
	storageSegments    map[string]*StorageSegment
	logf               func(format string, args ...any)
	onRecordingChange  func(channel uint16, recording bool, t uint64)
	onConnectionChange func(channel uint16, connected bool)
	onConnectFailed    func(channel uint16, err error)

	// retainedRTSPBytes holds bytes received by channels that are no
	// longer running, keyed by StorageID -- RemoveChannel folds a
	// departing ChannelIngest's RTSPBytesReceived() in here before
	// discarding it, so a channel edit (ReplaceChannel: remove-then-add,
	// a fresh ChannelIngest under the same id) or removal doesn't drop
	// farc_rtsp_bytes_received_total, which Prometheus would otherwise
	// read as a counter reset.
	retainedRTSPBytes map[string]int64
}

// NewIngestManager creates an empty IngestManager.
func NewIngestManager() *IngestManager {
	return &IngestManager{
		channels:           make(map[uint16]*channelEntry),
		storageSegments:    make(map[string]*StorageSegment),
		logf:               func(string, ...any) {},
		onRecordingChange:  func(uint16, bool, uint64) {},
		onConnectionChange: func(uint16, bool) {},
		onConnectFailed:    func(uint16, error) {},
		retainedRTSPBytes:  make(map[string]int64),
	}
}

// segmentForLocked returns cfg.StorageID's shared StorageSegment, creating
// it lazily on first use. Keyed purely by StorageID -- if a caller somehow
// supplies a different SegmentBackend for an already-registered StorageID,
// the existing entry silently wins (matches this package's existing
// lightweight-validation style elsewhere).
func (m *IngestManager) segmentForLocked(cfg ChannelConfig) *StorageSegment {
	if seg, ok := m.storageSegments[cfg.StorageID]; ok {
		return seg
	}
	seg := newStorageSegment(cfg.SegmentBackend)
	m.storageSegments[cfg.StorageID] = seg
	return seg
}

// SetLogger sets a callback for non-fatal diagnostics, forwarded to every
// ChannelIngest this manager creates from then on.
func (m *IngestManager) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	m.logf = logf
}

// SetOnRecordingChange installs a hook applied to every channel's
// CapturePolicy from then on (existing channels are unaffected -- call this
// before Start/AddChannel, as internal/farcd does). See
// CapturePolicy.SetOnRecordingChange for when it fires. A nil fn resets to
// a no-op.
func (m *IngestManager) SetOnRecordingChange(fn func(channel uint16, recording bool, t uint64)) {
	if fn == nil {
		fn = func(uint16, bool, uint64) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onRecordingChange = fn
}

// SetOnConnectionChange installs a hook applied to every channel's
// ChannelIngest from then on (existing channels are unaffected -- call this
// before Start/AddChannel, as internal/farcd does). See
// ChannelIngest.SetOnConnectionChange for when it fires. A nil fn resets to
// a no-op.
func (m *IngestManager) SetOnConnectionChange(fn func(channel uint16, connected bool)) {
	if fn == nil {
		fn = func(uint16, bool) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnectionChange = fn
}

// SetOnConnectFailed installs a hook applied to every channel's
// ChannelIngest from then on (existing channels are unaffected -- call this
// before Start/AddChannel, as internal/farcd does). See
// ChannelIngest.SetOnConnectFailed for when it fires. A nil fn resets to a
// no-op.
func (m *IngestManager) SetOnConnectFailed(fn func(channel uint16, err error)) {
	if fn == nil {
		fn = func(uint16, error) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnectFailed = fn
}

// Start creates and runs a ChannelIngest for each cfg in the background.
// At farcd startup, IngestManager creates each ChannelIngest with the
// CapturePolicy from its channel's configuration (§7); this is the entry
// point for that.
func (m *IngestManager) Start(cfgs []ChannelConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cfg := range cfgs {
		m.startLocked(cfg)
	}
}

func (m *IngestManager) startLocked(cfg ChannelConfig) {
	seg := m.segmentForLocked(cfg)
	policy := NewCapturePolicy(cfg.Channel, seg, cfg.QueueDepth, cfg.PolicyType, cfg.PolicyParams)
	policy.SetOnRecordingChange(m.onRecordingChange)
	ci := NewChannelIngest(cfg.Channel, policy)
	ci.SetLogger(m.logf)
	ci.SetBackpressureSignal(cfg.BackpressureSignal)
	ci.SetOnConnectionChange(m.onConnectionChange)
	ci.SetOnConnectFailed(m.onConnectFailed)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := ci.Run(ctx, cfg.RTSPURL, cfg.ReadTimeout, cfg.WriteTimeout)
		if err != nil {
			levellog.New(m.logf).Error("ingest: channel %d stopped: %v", cfg.Channel, err)
		}
	}()
	m.channels[cfg.Channel] = &channelEntry{cfg: cfg, ingest: ci, cancel: cancel, done: done}
}

// List returns every running channel's info, sorted by channel id.
// PolicyType/PolicyParams are read live off each channel's CapturePolicy
// (not the cfg it was started/last replaced with), since SetPolicy can
// change them without going through AddChannel/RemoveChannel.
func (m *IngestManager) List() []ChannelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChannelInfo, 0, len(m.channels))
	for _, e := range m.channels {
		policyType, params := e.ingest.policy.Policy()
		out = append(out, ChannelInfo{
			Channel:          e.cfg.Channel,
			RTSPURL:          e.cfg.RTSPURL,
			StorageID:        e.cfg.StorageID,
			PolicyType:       policyType,
			PolicyParams:     params,
			Name:             e.cfg.Name,
			Connected:        e.ingest.Connected(),
			Recording:        e.ingest.policy.Recording(),
			LastConnectError: e.ingest.LastConnectError(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

// StorageOf returns the storage id channel is currently assigned to -- a
// lightweight single-channel lookup (e.cfg.StorageID only, no
// e.ingest.policy.Policy() call) for callers that don't need List's full
// ChannelInfo (or its O(n log n) build-and-sort over every channel).
// CapturePolicy's own hooks (onRecordingChange/onConnectionChange) now fire
// after their mutex is released, so calling List from inside one is safe
// too -- StorageOf is no longer required there for deadlock avoidance, just
// convenient when only the storage id is needed.
func (m *IngestManager) StorageOf(channel uint16) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.channels[channel]
	if !ok {
		return "", false
	}
	return e.cfg.StorageID, true
}

// LiveTree returns channel's CapturePolicy.LiveSnapshot -- the fblock-live
// WS handler's (internal/api) entry point into a channel's in-progress
// segment tree. ok is false for an unknown channel id.
func (m *IngestManager) LiveTree(channel uint16) (LiveSnapshot, bool) {
	m.mu.Lock()
	e, ok := m.channels[channel]
	m.mu.Unlock()
	if !ok {
		return LiveSnapshot{}, false
	}
	return e.ingest.policy.LiveSnapshot(), true
}

// LiveTreeForStorage returns storageID's currently shared segment's whole
// tree plus its generation -- the fblock-tree admin page's (internal/api)
// entry point for a live (in_progress) fblock. Unlike LiveTree (per-
// channel), this needs no per-channel loop/merge: every channel of a
// Storage already shares one StorageSegment (storagesegment.go), so its own
// Elements()/Generation() already are the answer. ok is false if storageID
// has no channel configured on it (so no StorageSegment was ever created).
func (m *IngestManager) LiveTreeForStorage(storageID string) (elems []mediatree.Element, generation uint64, ok bool) {
	m.mu.Lock()
	seg, ok := m.storageSegments[storageID]
	m.mu.Unlock()
	if !ok {
		return nil, 0, false
	}
	return seg.Elements(), seg.Generation(), true
}

// AddChannel starts a single new channel while others may already be
// running -- the runtime counterpart to Start, for a channel created after
// startup (POST /channels). Returns an error if the channel id is already
// running.
func (m *IngestManager) AddChannel(cfg ChannelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.channels[cfg.Channel]; exists {
		return fmt.Errorf("ingest: channel %d already running", cfg.Channel)
	}
	m.startLocked(cfg)
	return nil
}

// RemoveChannel cancels channel's ingest loop and waits for it to finish
// before removing it, returning the ChannelConfig it was running with (so
// a caller -- e.g. an HTTP handler doing an edit as remove-then-add -- can
// restore it if a later step fails). Waiting happens outside the lock so
// other channels' SetPolicy/TriggerEvent calls aren't blocked while this
// one drains.
func (m *IngestManager) RemoveChannel(channel uint16) (ChannelConfig, error) {
	m.mu.Lock()
	e, ok := m.channels[channel]
	if !ok {
		m.mu.Unlock()
		return ChannelConfig{}, fmt.Errorf("ingest: unknown channel %d", channel)
	}
	delete(m.channels, channel)
	m.retainedRTSPBytes[e.cfg.StorageID] += e.ingest.RTSPBytesReceived()
	m.mu.Unlock()

	e.cancel()
	<-e.done
	return e.cfg, nil
}

// RTSPBytesReceivedForStorage returns the total raw RTP payload bytes ever
// received by any channel assigned to storageID -- both channels currently
// running and bytes retained from channels since removed or replaced (see
// retainedRTSPBytes's doc comment). internal/api sums this for
// farc_rtsp_bytes_received_total.
func (m *IngestManager) RTSPBytesReceivedForStorage(storageID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.retainedRTSPBytes[storageID]
	for _, e := range m.channels {
		if e.cfg.StorageID == storageID {
			total += e.ingest.RTSPBytesReceived()
		}
	}
	return total
}

// ReplaceChannel atomically (from the caller's point of view) swaps
// channel's running config for newCfg -- remove-then-add under the hood,
// since there's no cheap in-place path for a running channel's rtsp_url/
// storage (a different Storage means a different SegmentBackend). If
// starting newCfg fails (only plausible if something else raced to
// recreate this same id in the gap), the old config is restored before
// returning the error, so a failed call never leaves the channel stopped.
// Returns the config that was running before the swap, regardless of
// outcome, so a caller with its own further steps (e.g. persisting the new
// config to disk) can roll back to it too if one of those fails.
func (m *IngestManager) ReplaceChannel(channel uint16, newCfg ChannelConfig) (ChannelConfig, error) {
	old, err := m.RemoveChannel(channel)
	if err != nil {
		return ChannelConfig{}, err
	}
	err = m.AddChannel(newCfg)
	if err != nil {
		_ = m.AddChannel(old)
		return old, err
	}
	return old, nil
}

// SetPolicy implements §6 "смена политики" for an already-running channel:
// swaps its CapturePolicy strategy in place without touching the RTSP
// session or ChannelIngest goroutine.
func (m *IngestManager) SetPolicy(channel uint16, policyType PolicyType, params PolicyParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.channels[channel]
	if !ok {
		return fmt.Errorf("ingest: unknown channel %d", channel)
	}
	e.ingest.policy.SetPolicy(policyType, params)
	return nil
}

// TriggerEvent forwards a one-shot event trigger (§5.2's `event_time`) to
// channel's CapturePolicy — the `POST /channels/{id}/events` route's entry
// point. Only meaningful under the event policy; CapturePolicy.Trigger
// itself is the one that rejects it otherwise (ErrWrongPolicyType).
func (m *IngestManager) TriggerEvent(channel uint16, now, eventTime uint64) error {
	m.mu.Lock()
	e, ok := m.channels[channel]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("ingest: unknown channel %d", channel)
	}
	return e.ingest.policy.Trigger(now, eventTime)
}

// StartRecording forwards continuous's "начать запись [with from_time]"
// (§5.1) to channel's CapturePolicy -- the `POST /channels/{id}/recording/
// start` route's entry point. Only meaningful under the continuous policy;
// CapturePolicy.StartRecording itself is the one that rejects it otherwise
// (ErrWrongPolicyType).
func (m *IngestManager) StartRecording(channel uint16, now uint64, fromTime *uint64) error {
	m.mu.Lock()
	e, ok := m.channels[channel]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("ingest: unknown channel %d", channel)
	}
	return e.ingest.policy.StartRecording(now, fromTime)
}

// StopRecording forwards continuous's "остановить запись" (§5.1) to
// channel's CapturePolicy -- the `POST /channels/{id}/recording/stop`
// route's entry point.
func (m *IngestManager) StopRecording(channel uint16, now uint64) error {
	m.mu.Lock()
	e, ok := m.channels[channel]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("ingest: unknown channel %d", channel)
	}
	return e.ingest.policy.StopRecording(now)
}

// Stop cancels every channel's ingest loop and waits for all of them to
// finish.
func (m *IngestManager) Stop() {
	m.mu.Lock()
	entries := make([]*channelEntry, 0, len(m.channels))
	for _, e := range m.channels {
		e.cancel()
		entries = append(entries, e)
	}
	m.mu.Unlock()

	for _, e := range entries {
		<-e.done
	}
}
