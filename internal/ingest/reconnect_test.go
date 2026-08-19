package ingest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/headers"
)

// scriptedSource is an rtspSource whose Start/Describe/Play/Wait behavior
// is entirely scripted, so a test can drive ChannelIngest.runReconnecting
// through a controlled sequence of failures/successes without any real
// network.
type scriptedSource struct {
	describeErr error
	desc        *description.Session
	waitErr     error // if set, Wait returns it immediately; otherwise Wait blocks until Close
	closed      chan struct{}
	closeOnce   sync.Once
}

func (s *scriptedSource) Start() error { return nil }

func (s *scriptedSource) Close() {
	s.closeOnce.Do(func() { close(s.closed) })
}

func (s *scriptedSource) Wait() error {
	if s.waitErr != nil {
		return s.waitErr
	}
	<-s.closed
	return nil
}

func (s *scriptedSource) Describe(*base.URL) (*description.Session, *base.Response, error) {
	if s.describeErr != nil {
		return nil, nil, s.describeErr
	}
	return s.desc, nil, nil
}

func (s *scriptedSource) Setup(*base.URL, *description.Media, int, int) (*base.Response, error) {
	return nil, nil
}

func (s *scriptedSource) Play(*headers.Range) (*base.Response, error) { return nil, nil }

func (s *scriptedSource) OnPacketRTP(*description.Media, format.Format, gortsplib.OnPacketRTPFunc) {}

// TestChannelIngest_ReconnectsWithBackoffAndFiresConnectionEvents drives
// runReconnecting through: (1) a Describe failure (simulating the camera
// being unreachable at all -- this is also the "AddChannel already
// returned success, the RTSP work happens fully async" scenario), (2) a
// successful connect that then drops mid-session, (3) a successful,
// stable reconnect that stays up until the test cancels it. It asserts the
// loop retries past both failures with backoff and that
// channel.rtsp.connected/disconnected fire at exactly the right moments
// (never on the final, deliberate shutdown).
func TestChannelIngest_ReconnectsWithBackoffAndFiresConnectionEvents(t *testing.T) {
	origInitial, origMax := reconnectInitialBackoff, reconnectMaxBackoff
	reconnectInitialBackoff, reconnectMaxBackoff = time.Millisecond, 5*time.Millisecond
	defer func() { reconnectInitialBackoff, reconnectMaxBackoff = origInitial, origMax }()

	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(time.Second), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	var mu sync.Mutex
	var events []bool
	ci.SetOnConnectionChange(func(channel uint16, connected bool) {
		mu.Lock()
		events = append(events, connected)
		mu.Unlock()
	})

	desc := &description.Session{Medias: []*description.Media{{
		Type:    description.MediaTypeAudio,
		Formats: []format.Format{&format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1}},
	}}}

	var attempts int32
	newSource := func() (rtspSource, *base.URL, error) {
		n := atomic.AddInt32(&attempts, 1)
		switch n {
		case 1:
			// Camera unreachable -- describe fails, nothing ever connects.
			return &scriptedSource{describeErr: errors.New("connection refused"), closed: make(chan struct{})}, &base.URL{}, nil
		case 2:
			// Connects, then the session drops.
			return &scriptedSource{desc: desc, waitErr: errors.New("connection reset"), closed: make(chan struct{})}, &base.URL{}, nil
		default:
			// Connects and stays up until the test cancels ctx.
			return &scriptedSource{desc: desc, closed: make(chan struct{})}, &base.URL{}, nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- ci.runReconnecting(ctx, newSource) }()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("did not observe 3 connection events in time, got %v", events)
			mu.Unlock()
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("runReconnecting: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runReconnecting did not return after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []bool{true, false, true}
	if len(events) != len(want) {
		t.Fatalf("connection events = %v, want %v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("connection events = %v, want %v", events, want)
		}
	}
	if got := atomic.LoadInt32(&attempts); got < 3 {
		t.Fatalf("newSource call count = %d, want >= 3 (must have retried past both failures)", got)
	}
}
