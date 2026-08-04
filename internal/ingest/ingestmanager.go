package ingest

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ChannelConfig is one configured channel's ingest setup (docs/docs/archive/
// 04-storage-operations.md §2.1's channel list + 10-capture-policy.md §7's
// capture-policy fields). Loading/validating this from the process's JSON
// config file is internal/config's job (Phase 11), not this package's --
// IngestManager just needs the resolved values.
type ChannelConfig struct {
	Channel      uint16
	RTSPURL      string
	Recorder     Recorder
	QueueDepth   uint64 // ns
	PolicyType   PolicyType
	PolicyParams PolicyParams
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type channelEntry struct {
	ingest *ChannelIngest
	cancel context.CancelFunc
	done   chan struct{}
}

// IngestManager creates and owns one ChannelIngest per configured channel
// (docs/docs/archive/11-service-composition.md §5.1.1).
type IngestManager struct {
	mu       sync.Mutex
	channels map[uint16]*channelEntry
	logf     func(format string, args ...any)
}

// NewIngestManager creates an empty IngestManager.
func NewIngestManager() *IngestManager {
	return &IngestManager{
		channels: make(map[uint16]*channelEntry),
		logf:     func(string, ...any) {},
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
	ci := NewChannelIngest(cfg.Channel, policy)
	ci.SetLogger(m.logf)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := ci.Run(ctx, cfg.RTSPURL, cfg.ReadTimeout, cfg.WriteTimeout); err != nil {
			m.logf("ingest: channel %d stopped: %v", cfg.Channel, err)
		}
	}()
	m.channels[cfg.Channel] = &channelEntry{ingest: ci, cancel: cancel, done: done}
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
