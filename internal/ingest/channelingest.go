package ingest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/base"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/levellog"
)

// ChannelIngest is one channel's RTSP ingest loop: connects, sets up every
// supported media/format, and feeds decoded frames into its CapturePolicy
// (docs/docs/archive/11-service-composition.md §5.1.1), reconnecting with
// backoff (Run) on any session failure until stopped. One instance per
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
	// already-imposed limit by not calling HandleFrame at all while
	// shedding. For audio (no GOP concept) that's this value checked
	// directly, per frame; for video, rtsp.go's gopShedGate only calls this
	// when a keyframe arrives and applies that one answer to the whole GOP
	// (.scratch/capture-keyframe-start/issues/02-backpressure-gop-aware-
	// shedding.md), so a GOP is never split between recorded and dropped.
	// Defaults to "never skip" so a ChannelIngest with no wired signal
	// (e.g. every existing test) behaves exactly as before.
	skipFrames func() bool

	logf func(format string, args ...any)

	// connMu guards connected -- read by Connected() (IngestManager.List's
	// GET /channels status field) and written by setConnected, which fires
	// from Run's reconnect loop, concurrently with any caller reading it.
	connMu    sync.Mutex
	connected bool
	// onConnectionChange fires on every actual connected flip (mirrors
	// CapturePolicy.onRecordingChange's own convention), called after connMu
	// is released -- safe to call back into ci from within the hook.
	// Defaults to a no-op so a ChannelIngest with no wired signal (e.g.
	// every existing test) behaves exactly as before.
	onConnectionChange func(channel uint16, connected bool)
}

// NewChannelIngest creates a ChannelIngest for channel, writing through
// policy (already configured with its CapturePolicy strategy).
func NewChannelIngest(channel uint16, policy *CapturePolicy) *ChannelIngest {
	return &ChannelIngest{
		channel:            channel,
		policy:             policy,
		now:                time.Now,
		skipFrames:         func() bool { return false },
		logf:               func(string, ...any) {},
		onConnectionChange: func(uint16, bool) {},
	}
}

// SetBackpressureSignal wires fn as the channel's "skip frames" check
// (see the skipFrames field doc). internal/farcd supplies a closure reading
// the channel's target Storage's current buffer-pool status
// (storage.Pool.Status() == storage.PoolBackpressure, via
// StorageUnit.PoolStatus()) — no separate atomic flag or transition-
// tracking is needed, since Status() itself is already a cheap, live,
// mutex-guarded read. This supersedes the write-queue-depth-based
// StorageEngine.Level() signal, which is metrics-only today. rtsp.go's
// gopShedGate is what actually calls fn (only at GOP boundaries, for
// video) -- not this method. A nil argument restores the default no-op
// (never skip).
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

// SetOnConnectionChange installs a hook fired every time the RTSP session's
// connected state actually flips (mirrors CapturePolicy.SetOnRecordingChange),
// called after ci's own connMu is released -- safe to call back into ci
// from within the hook. A nil argument restores the default no-op.
func (ci *ChannelIngest) SetOnConnectionChange(fn func(channel uint16, connected bool)) {
	if fn == nil {
		fn = func(uint16, bool) {}
	}
	ci.onConnectionChange = fn
}

// Connected reports whether the RTSP session is currently established --
// IngestManager.List's GET /channels status field.
func (ci *ChannelIngest) Connected() bool {
	ci.connMu.Lock()
	defer ci.connMu.Unlock()
	return ci.connected
}

func (ci *ChannelIngest) setConnected(connected bool) {
	ci.connMu.Lock()
	if ci.connected == connected {
		ci.connMu.Unlock()
		return
	}
	ci.connected = connected
	hook, channel := ci.onConnectionChange, ci.channel
	ci.connMu.Unlock()
	hook(channel, connected)
}

// reconnectInitialBackoff/reconnectMaxBackoff bound Run's reconnect loop
// (docs/docs/archive/11-service-composition.md §5.1.1's "переподключается
// при разрыве RTSP-сессии") -- exponential, capped, matching
// internal/msmd's own external-reconnect convention: an RTSP camera,
// unlike farcd's own internal services, cannot be assumed "almost always
// reachable" (contrast internal/tocindex/internal/hlsd's fixed-delay
// choice, justified there specifically because farcd is). Vars, not
// consts, so tests can shrink them instead of waiting out real backoff
// delays.
var (
	reconnectInitialBackoff = time.Second
	reconnectMaxBackoff     = 30 * time.Second
)

// Run connects to rtspURL and streams frames into ci's CapturePolicy,
// reconnecting with capped exponential backoff on any session failure,
// until ctx is cancelled. A fresh rtspSource is dialed for every attempt,
// but ci itself (and therefore its CapturePolicy, including cachedParams --
// see reportVideoParams/reportAudioParams in rtsp.go) is reused across
// every reconnect, so a config comparison against pre-disconnect params is
// possible without any extra plumbing.
func (ci *ChannelIngest) Run(ctx context.Context, rtspURL string, readTimeout, writeTimeout time.Duration) error {
	return ci.runReconnecting(ctx, func() (rtspSource, *base.URL, error) {
		return NewClient(rtspURL, readTimeout, writeTimeout)
	})
}

// runReconnecting is Run's actual reconnect loop, taking an rtspSource
// factory so tests can drive it with fakes across multiple attempts
// without a real network -- Run itself is just this plus a closure over
// NewClient.
func (ci *ChannelIngest) runReconnecting(ctx context.Context, newSource func() (rtspSource, *base.URL, error)) error {
	backoff := reconnectInitialBackoff
	for {
		c, u, err := newSource()
		if err != nil {
			return err // malformed URL -- retrying won't help, fail the whole channel
		}

		runErr := ci.run(ctx, c, u)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // deliberate shutdown (RemoveChannel/farcd stop): ctx cancellation is not a session failure, so runErr (likely just ctx.Err() wrapped) is not this call's error to report
		}
		ci.setConnected(false)
		levellog.New(ci.logf).Warn("ingest: channel %d: rtsp session ended, reconnecting in %s: %v", ci.channel, backoff, runErr)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > reconnectMaxBackoff {
			backoff = reconnectMaxBackoff
		}
	}
}

// run drives one RTSP session over an already-configured rtspSource, for
// its whole lifetime (Start through disconnect or ctx cancellation) —
// split out from Run so tests can supply a fake, or (the one real
// end-to-end test) a real *gortsplib.Client pointed at an in-process
// loopback server. Run calls this once per reconnect attempt.
func (ci *ChannelIngest) run(ctx context.Context, c rtspSource, u *base.URL) error {
	err := c.Start(u.Scheme, u.Host)
	if err != nil {
		return fmt.Errorf("ingest: channel %d: start: %w", ci.channel, err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return fmt.Errorf("ingest: channel %d: describe: %w", ci.channel, err)
	}

	// One RTSP link is one stream (.scratch/fblocks-ui/issues/
	// 07-one-stream-per-channel-video-and-audio.md): every media in this
	// Describe's session shares streamNum 0, so a channel's video and audio
	// land under the same RoleStream tree node instead of one per track.
	// "streams" (plural) is reserved for a future multiple-RTSP-link-per-
	// channel case, not implemented today.
	const streamNum uint32 = 0
	seenKind := make(map[fcontainer.StreamKind]bool)
	setup := false
	for _, medi := range desc.Medias {
		if medi.IsBackChannel {
			continue
		}
		_, err = c.Setup(desc.BaseURL, medi, 0, 0)
		if err != nil {
			return fmt.Errorf("ingest: channel %d: setup: %w", ci.channel, err)
		}
		ci.setupMedia(c, medi, streamNum, seenKind)
		setup = true
	}
	if !setup {
		return fmt.Errorf("ingest: channel %d: no usable media in RTSP description", ci.channel)
	}

	_, err = c.Play(nil)
	if err != nil {
		return fmt.Errorf("ingest: channel %d: play: %w", ci.channel, err)
	}
	ci.setConnected(true)

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
			err := ci.policy.Tick(ci.nowNS())
			if err != nil {
				levellog.New(ci.logf).Warn("ingest: channel %d: tick: %v", ci.channel, err)
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
