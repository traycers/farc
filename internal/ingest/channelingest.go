package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/base"
)

// ChannelIngest is one channel's RTSP ingest loop: connects, sets up every
// supported media/format, and feeds decoded frames into its CapturePolicy
// (docs/docs/archive/11-service-composition.md §5.1.1). One instance per
// configured channel, created by IngestManager.
type ChannelIngest struct {
	channel uint16
	policy  *CapturePolicy

	// now is the frame timestamp source. Frame.Time is documented as
	// absolute Unix ns (fcontainer.Frame), but the docs never specify how
	// ChannelIngest should derive it from RTSP — real cameras frequently
	// ship no usable RTCP sender reports to synchronize an NTP timestamp
	// (gortsplib.Client.PacketNTP), which would otherwise be the natural
	// choice. Using wall-clock arrival time instead is a deliberate v1
	// simplification: always available, monotonic enough for continuous/
	// event-window purposes, injectable here for deterministic tests.
	now func() time.Time

	// skipFrames is the read-only half of the StorageUnit -> CapturePolicy
	// backpressure signal (docs/docs/archive/10-capture-policy.md §8's own
	// open question, resolved in PLAN.md's gap-resolutions section):
	// ChannelIngest doesn't decide what to shed, it just executes an
	// already-imposed limit by not calling HandleFrame at all while this
	// returns true. Defaults to "never skip" so a ChannelIngest with no
	// wired signal (e.g. every existing test) behaves exactly as before.
	skipFrames func() bool

	logf func(format string, args ...any)
}

// NewChannelIngest creates a ChannelIngest for channel, writing through
// policy (already configured with its CapturePolicy strategy).
func NewChannelIngest(channel uint16, policy *CapturePolicy) *ChannelIngest {
	return &ChannelIngest{
		channel:    channel,
		policy:     policy,
		now:        time.Now,
		skipFrames: func() bool { return false },
		logf:       func(string, ...any) {},
	}
}

// SetBackpressureSignal wires fn as the channel's "skip frames" check
// (see the skipFrames field doc). internal/farcd supplies a closure polling
// the channel's target Storage's current StorageEngine.Level() — no
// separate atomic flag or transition-tracking is needed, since Level()
// itself is already a cheap, live, mutex-guarded read. A nil argument
// restores the default no-op (never skip).
func (ci *ChannelIngest) SetBackpressureSignal(fn func() bool) {
	if fn == nil {
		fn = func() bool { return false }
	}
	ci.skipFrames = fn
}

// SetLogger sets a callback for non-fatal diagnostics (unsupported
// formats, per-frame decode errors that don't stop the channel). A nil
// argument restores the default no-op.
func (ci *ChannelIngest) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ci.logf = logf
}

func (ci *ChannelIngest) nowNS() uint64 {
	return uint64(ci.now().UnixNano())
}

// Run connects to rtspURL and streams frames into ci's CapturePolicy until
// ctx is cancelled or a fatal error occurs.
func (ci *ChannelIngest) Run(ctx context.Context, rtspURL string, readTimeout, writeTimeout time.Duration) error {
	c, u, err := NewClient(rtspURL, readTimeout, writeTimeout)
	if err != nil {
		return err
	}
	return ci.run(ctx, c, u)
}

// run drives an already-configured rtspSource — split out from Run so
// tests can supply a fake, or (the one real end-to-end test) a real
// *gortsplib.Client pointed at an in-process loopback server.
func (ci *ChannelIngest) run(ctx context.Context, c rtspSource, u *base.URL) error {
	if err := c.Start(); err != nil {
		return fmt.Errorf("ingest: channel %d: start: %w", ci.channel, err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return fmt.Errorf("ingest: channel %d: describe: %w", ci.channel, err)
	}

	var streamNum uint32
	setup := false
	for _, medi := range desc.Medias {
		if medi.IsBackChannel {
			continue
		}
		if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
			return fmt.Errorf("ingest: channel %d: setup: %w", ci.channel, err)
		}
		ci.setupMedia(c, medi, streamNum)
		streamNum++
		setup = true
	}
	if !setup {
		return fmt.Errorf("ingest: channel %d: no usable media in RTSP description", ci.channel)
	}

	if _, err := c.Play(nil); err != nil {
		return fmt.Errorf("ingest: channel %d: play: %w", ci.channel, err)
	}

	readErr := make(chan error, 1)
	go func() { readErr <- c.Wait() }()

	// event's stop_at (docs/docs/archive/10-capture-policy.md §5.2) must
	// fire even with no incoming frames -- a periodic Tick, not just a
	// reaction to frames, is exactly what the docs call for.
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-readErr:
			return err
		case <-ticker.C:
			if err := ci.policy.Tick(ci.nowNS()); err != nil {
				ci.logf("ingest: channel %d: tick: %v", ci.channel, err)
			}
		case <-ctx.Done():
			c.Close()
			<-readErr
			return ci.policy.Close(ci.nowNS())
		}
	}
}

// tickInterval is how often ChannelIngest.run checks CapturePolicy.Tick.
const tickInterval = time.Second
