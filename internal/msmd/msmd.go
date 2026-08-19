// Package msmd is msm_server's process wiring for both integration
// directions with the external msm/controller system:
//
//   - Outbound: subscribes to one farcd's WS event feed (internal/msmclient)
//     and, for every event, makes the matching outbound call(s) to the
//     external msm service (internal/msmapi), converting the TOC msm_server
//     receives over WS into vaa-blocks and stream params
//     (internal/vaablocks) along the way.
//   - Inbound: runs internal/archivesapi's HTTP server, translating the
//     external msm/controller's /api/v1/archives/* calls
//     (temp/controller/openapi.yaml) into calls against farcd's own generic
//     Storage/Channel API via internal/farcctl -- the single integration
//     point into this codebase, per the 2026-08-13 decision to move that
//     route group out of farcd.
//
// The outbound side's delivery is best-effort, matching farcd's own WS feed
// (internal/api/eventpush.go's documented "no reconnect catch-up" policy):
// Run redials on disconnect and picks up wherever the reconnected feed
// resumes, but never persists a queue or retries a call across a
// reconnect. Events are processed strictly one at a time (a single
// goroutine per connection), so a vaa_blocks_add for a given fblock is
// always sent, and completes, before the info_set that follows it in the
// same fblock.ready handling -- the ordering temp/msm/openapi.yaml
// documents, achieved here just by not doing anything concurrently, not by
// any explicit sequencing mechanism. The inbound side is a synchronous
// per-request translation with no queue of its own either.
package msmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/archivesapi"
	"github.com/traycers/farc/internal/farcctl"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/internal/msmapi"
	"github.com/traycers/farc/internal/msmclient"
	"github.com/traycers/farc/internal/msmconfig"
	"github.com/traycers/farc/internal/vaablocks"
	"github.com/traycers/farc/toc"
)

// readHeaderTimeout bounds how long the archivesapi http.Server waits for a
// client to finish sending request headers, matching internal/farcd's own
// constant of the same purpose.
const readHeaderTimeout = 10 * time.Second

// shutdownTimeout bounds how long the archivesapi http.Server's graceful
// shutdown waits for in-flight requests to finish, matching internal/farcd's
// own constant of the same purpose.
const shutdownTimeout = 10 * time.Second

// Event name constants, duplicated from internal/api/eventpush.go rather
// than imported -- internal/msmclient's own doc comment explains why this
// package doesn't depend on internal/api directly (same reasoning
// internal/hlsclient already applies to its own Event* constants).
const (
	eventFblockCreated    = "fblock.created"
	eventFblockReady      = "fblock.ready"
	eventFblockDeleted    = "fblock.deleted"
	eventRecordingStarted = "channel.recording.started"
	eventRecordingStopped = "channel.recording.stopped"
)

// msm's models.stream.type enum (temp/msm/openapi.yaml).
const (
	streamTypeVideo = 1
	streamTypeAudio = 2
)

// tocWaitTimeout bounds how long handleFblockReady waits for the "toc" frame
// that internal/api/eventpush.go's serveGlobal always sends immediately
// after a fblock.ready event to an IncludeTOC subscriber, on the same
// connection -- a value this large arriving is a bug (in farcd or here),
// not a real timing race, so this is a safety net, not a tuning knob.
const tocWaitTimeout = 30 * time.Second

// outbound is internal/msmapi.Client's method set -- an interface so tests
// can substitute a fake instead of making real HTTP calls.
type outbound interface {
	ParamsAdd(ctx context.Context, aid string, id int64, streamType int, data any) error
	FblocksAdd(ctx context.Context, aid string, id [16]byte, num int64) error
	FblocksDel(ctx context.Context, aid string, id [16]byte) error
	InfoSet(ctx context.Context, aid string, fbid [16]byte, num int64, status int, start, stop time.Time) error
	StartedAdd(ctx context.Context, aid string, channel uint16, begin time.Time) error
	FinishedAdd(ctx context.Context, aid string, channel uint16, end time.Time) error
	VaaBlocksAdd(ctx context.Context, aid string, block msmapi.VaaBlock) error
}

// contentFetcher is internal/hlsclient.Client's ReadRanges method -- see
// outbound's own doc comment.
type contentFetcher interface {
	ReadRanges(ctx context.Context, storageID string, uuid [16]byte, ranges []hlsclient.Range) ([][]byte, error)
}

// subscriber is internal/msmclient.Client's Subscribe method -- see
// outbound's own doc comment.
type subscriber interface {
	Subscribe(ctx context.Context) (<-chan msmclient.Event, error)
}

// processor holds the one piece of state this whole package keeps:
// which (archive, channel, stream, config-time) combinations have already
// been reported via params_add, and under which minted id -- see
// ensureParams's own doc comment for why config-time is a sufficient dedup
// key. Not safe for concurrent use -- consume's single-goroutine event loop
// is this type's only caller.
type processor struct {
	out     outbound
	content contentFetcher
	logf    func(format string, args ...any)

	paramsSeen   map[paramsKey]int64
	nextParamsID int64
}

func newProcessor(out outbound, content contentFetcher, logf func(string, ...any)) *processor {
	return &processor{out: out, content: content, logf: logf, paramsSeen: make(map[paramsKey]int64)}
}

// Run subscribes to cfg's farcd and processes events until ctx is
// cancelled, redialing with exponential backoff (capped at 30s) on any
// disconnect or subscribe failure -- concurrently with cfg.HTTP's
// archivesapi server (the external msm/controller's single integration
// point, per the 2026-08-13 decision to move /api/v1/archives/* out of
// farcd and into msm_server). Either subsystem failing tears both down;
// Run returns once both have stopped.
func Run(ctx context.Context, cfg *msmconfig.Config, logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	sub := msmclient.New(cfg.FarcWS)
	out := msmapi.New(cfg.MSMBaseURL)
	content := hlsclient.New(cfg.FarcHTTP, "")
	p := newProcessor(out, content, logf)

	client := farcctl.New(cfg.FarcHTTP)
	httpSrv := &http.Server{
		Addr:              cfg.HTTP.String(),
		Handler:           archivesapi.New(client).Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	var wsConnected atomic.Bool
	metricsSrv := &http.Server{
		Addr:              cfg.Metrics.String(),
		Handler:           newMetricsHandler(&wsConnected),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// runCtx is run's own cancellation, derived from ctx so a normal
	// shutdown (ctx cancelled by the caller) stops it automatically, but
	// also cancellable on its own if the archives HTTP server fails to
	// start (below) -- ctx itself isn't ours to cancel.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	errCh := make(chan error, 2)
	go func() {
		err := httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("msmd: archives http server: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		err := metricsSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("msmd: metrics server: %w", err)
			return
		}
		errCh <- nil
	}()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		run(runCtx, sub, p, logf, &wsConnected)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			levellog.New(logf).Error("%v", err)
		}
		cancelRun()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range []*http.Server{httpSrv, metricsSrv} {
		err := srv.Shutdown(shutdownCtx) //nolint:contextcheck // deliberate: ctx may already be Done() here, so shutdown builds its own fresh timeout context rather than reusing a cancelled one -- matches internal/farcd's identical pattern
		if err != nil {
			levellog.New(logf).Error("msmd: archives http server shutdown: %v", err)
		}
	}
	<-runDone
}

func run(ctx context.Context, sub subscriber, p *processor, logf func(string, ...any), wsConnected *atomic.Bool) {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for ctx.Err() == nil {
		events, err := sub.Subscribe(ctx)
		if err != nil {
			wsConnected.Store(false)
			levellog.New(logf).Warn("msmd: subscribe: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		wsConnected.Store(true)
		levellog.New(logf).Info("msmd: connected to farcd's event feed")
		p.consume(ctx, events)
		wsConnected.Store(false)
		levellog.New(logf).Warn("msmd: disconnected from farcd's event feed, reconnecting")
	}
}

// consume processes events sequentially until it closes (disconnect) or ctx
// is cancelled. A handling error is logged and processing continues with
// the next event -- best-effort, matching this package's own doc comment.
func (p *processor) consume(ctx context.Context, events <-chan msmclient.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type != "event" {
				// A stray "toc" frame not consumed by handleFblockReady
				// (e.g. one that arrived after this package gave up
				// waiting for it) -- nothing to do with it on its own.
				continue
			}
			err := p.handle(ctx, ev, events)
			if err != nil {
				levellog.New(p.logf).Warn("msmd: handling %s (storage=%s channel=%d): %v", ev.Name, ev.Storage, ev.Channel, err)
			}
		}
	}
}

func (p *processor) handle(ctx context.Context, ev msmclient.Event, events <-chan msmclient.Event) error {
	switch ev.Name {
	case eventFblockCreated:
		if !ev.HasUUID {
			return errors.New("fblock.created: missing uuid")
		}
		return p.out.FblocksAdd(ctx, ev.Storage, ev.UUID, int64(ev.Index))
	case eventFblockDeleted:
		if !ev.HasUUID {
			return errors.New("fblock.deleted: missing uuid")
		}
		return p.out.FblocksDel(ctx, ev.Storage, ev.UUID)
	case eventFblockReady:
		return p.handleFblockReady(ctx, ev, events)
	case eventRecordingStarted:
		if ev.Storage == "" {
			return errors.New("channel.recording.started: missing storage (archive) id")
		}
		return p.out.StartedAdd(ctx, ev.Storage, ev.Channel, nsToTime(ev.Begin))
	case eventRecordingStopped:
		if ev.Storage == "" {
			return errors.New("channel.recording.stopped: missing storage (archive) id")
		}
		return p.out.FinishedAdd(ctx, ev.Storage, ev.Channel, nsToTime(ev.End))
	default:
		return nil
	}
}

// handleFblockReady reads the "toc" frame internal/api/eventpush.go's
// serveGlobal always sends immediately after a fblock.ready event to an
// IncludeTOC subscriber (same connection, next message -- see this
// package's own doc comment on why that ordering is safe to rely on),
// reports every channel's stream params and video vaa-blocks found in it,
// then info_sets the fblock as Ready -- always last, per every
// VaaBlocksAdd call for this fblock having already completed above.
func (p *processor) handleFblockReady(ctx context.Context, ev msmclient.Event, events <-chan msmclient.Event) error {
	if !ev.HasUUID {
		return errors.New("fblock.ready: missing uuid")
	}

	var tocEv msmclient.Event
	select {
	case next, ok := <-events:
		if !ok {
			return errors.New("connection closed waiting for the toc frame")
		}
		if next.Type != "toc" {
			return fmt.Errorf("expected a toc frame right after fblock.ready, got type %q", next.Type)
		}
		tocEv = next
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(tocWaitTimeout):
		return errors.New("timed out waiting for the toc frame")
	}

	columns, err := toc.Decode(tocEv.TOC)
	if err != nil {
		return fmt.Errorf("decode toc: %w", err)
	}

	for _, channel := range vaablocks.Channels(columns) {
		err := p.reportChannel(ctx, ev.Storage, int64(ev.Index), ev.UUID, columns, channel)
		if err != nil {
			return fmt.Errorf("channel %d: %w", channel, err)
		}
	}

	return p.out.InfoSet(ctx, ev.Storage, ev.UUID, int64(ev.Index), int(fblock.Ready), nsToTime(ev.Begin), nsToTime(ev.End))
}

// reportChannel params_adds every stream config version present for channel
// (video and audio alike), then vaa_blocks_add's every computed vaa-block of
// both kinds -- video and audio each have their own, independent
// gap-splitting timeline (.scratch/msm-integration/issues/
// 01-audio-vaa-blocks.md) -- referencing whichever config governed its
// first frame.
func (p *processor) reportChannel(ctx context.Context, aid string, fnum int64, fblockID [16]byte, columns *toc.Columns, channel uint16) error {
	configs, err := vaablocks.StreamConfigs(columns, channel)
	if err != nil {
		return fmt.Errorf("stream configs: %w", err)
	}
	configByID := make(map[uint32]vaablocks.StreamConfig, len(configs))
	for _, sc := range configs {
		_, err := p.ensureParams(ctx, aid, channel, fblockID, sc)
		if err != nil {
			return fmt.Errorf("params for config %d: %w", sc.ConfigID, err)
		}
		configByID[sc.ConfigID] = sc
	}

	kinds := [2]struct {
		kind       vaablocks.StreamKind
		streamType int
	}{
		{vaablocks.KindVideo, streamTypeVideo},
		{vaablocks.KindAudio, streamTypeAudio},
	}
	for _, k := range kinds {
		blocks, err := vaablocks.Compute(columns, channel, k.kind)
		if err != nil {
			return fmt.Errorf("compute vaa-blocks: %w", err)
		}
		for _, b := range blocks {
			sc, ok := configByID[b.ConfigID]
			if !ok {
				return fmt.Errorf("vaa-block [%d,%d] references unknown config %d", b.Begin, b.End, b.ConfigID)
			}
			paramsID, err := p.ensureParams(ctx, aid, channel, fblockID, sc)
			if err != nil {
				return fmt.Errorf("params for config %d: %w", b.ConfigID, err)
			}
			err = p.out.VaaBlocksAdd(ctx, aid, msmapi.VaaBlock{
				ID:         msmapi.VaaBlockID{Fnum: fnum, Offset: int32(b.Offset), Size: int32(b.Size)},
				FblockID:   fblockID,
				ChannelNum: int32(b.Channel),
				ParamsID:   paramsID,
				StreamID:   int16(b.StreamID),
				StreamType: k.streamType,
				Begin:      nsToTime(b.Begin),
				End:        nsToTime(b.End),
			})
			if err != nil {
				return fmt.Errorf("vaa_blocks_add: %w", err)
			}
		}
	}
	return nil
}

func nsToTime(ns uint64) time.Time { return time.Unix(0, int64(ns)).UTC() }
