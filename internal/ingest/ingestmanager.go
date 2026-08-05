package ingest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
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
	StorageID    string
	Recorder     Recorder
	QueueDepth   uint64 // ns
	PolicyType   PolicyType
	PolicyParams PolicyParams
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

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
}

// IngestManager creates and owns one ChannelIngest per configured channel
// (docs/docs/archive/11-service-composition.md §5.1.1).
type IngestManager struct {
	mu                sync.Mutex
	channels          map[uint16]*channelEntry
	logf              func(format string, args ...any)
	onRecordingChange func(channel uint16, recording bool)
}

// NewIngestManager creates an empty IngestManager.
func NewIngestManager() *IngestManager {
	return &IngestManager{
		channels:          make(map[uint16]*channelEntry),
		logf:              func(string, ...any) {},
		onRecordingChange: func(uint16, bool) {},
	}
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
func (m *IngestManager) SetOnRecordingChange(fn func(channel uint16, recording bool)) {
	if fn == nil {
		fn = func(uint16, bool) {}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onRecordingChange = fn
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
	policy := NewCapturePolicy(cfg.Channel, cfg.Recorder, cfg.QueueDepth, cfg.PolicyType, cfg.PolicyParams)
	policy.SetOnRecordingChange(m.onRecordingChange)
	ci := NewChannelIngest(cfg.Channel, policy)
	ci.SetLogger(m.logf)
	ci.SetBackpressureSignal(cfg.BackpressureSignal)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := ci.Run(ctx, cfg.RTSPURL, cfg.ReadTimeout, cfg.WriteTimeout); err != nil {
			m.logf("ingest: channel %d stopped: %v", cfg.Channel, err)
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
			Channel:      e.cfg.Channel,
			RTSPURL:      e.cfg.RTSPURL,
			StorageID:    e.cfg.StorageID,
			PolicyType:   policyType,
			PolicyParams: params,
			Name:         e.cfg.Name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
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
	m.mu.Unlock()

	e.cancel()
	<-e.done
	return e.cfg, nil
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
