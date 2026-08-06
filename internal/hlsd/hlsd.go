// Package hlsd is hls_server's process wiring, mirroring internal/farcd's
// own New/SetLogger/Run/shutdown orchestrator shape: load hlsconfig ->
// build the one hlsclient.Client for cfg.Farcd (ADR-020) -> open the disk
// segment cache -> run a reconciliation loop that starts one
// tocindex.EventSubscriber per channel farcd currently has (ADR-018,
// ADR-021) and keeps that set converged to farcd's live GET /channels list
// for the process's whole lifetime, no restart needed -> serve
// internal/hlsapi on one listener -> graceful shutdown on context
// cancellation.
package hlsd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"traycers/farc/internal/hlsapi"
	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/hlsconfig"
	"traycers/farc/internal/segmentcache"
	"traycers/farc/internal/tocindex"
)

// shutdownTimeout bounds how long graceful HTTP shutdown waits for
// in-flight requests before Run returns anyway — matches internal/farcd's
// own constant.
const shutdownTimeout = 10 * time.Second

// readHeaderTimeout bounds how long the http.Server waits for a client to
// finish sending request headers (mitigates Slowloris-style slow-header
// attacks; net/http.Server has no timeout by default) — matches
// internal/farcd's own constant.
const readHeaderTimeout = 10 * time.Second

// reconcileRetryDelay bounds how soon reconcile retries after a failed
// GET /channels call or a dropped channel-lifecycle WS connection —
// matches tocindex.EventSubscriber's own fixed retry backoff.
const reconcileRetryDelay = 2 * time.Second

// channelRecheckInterval bounds how stale a *dropped* channel-lifecycle
// event (api.EventPushServer.PublishChannelEvent's drop-if-full policy) can
// leave this process while its WS connection stays healthy — reconcileOnce
// re-lists GET /channels on this cadence in addition to reacting to events.
const channelRecheckInterval = 30 * time.Second

// trackedSub is one channel's live subscription bookkeeping: the
// tocindex.EventSubscriber's own cancelable context and the storage it's
// currently reading from (so a later re-list can tell a genuine storage
// move from a no-op).
type trackedSub struct {
	cancel  context.CancelFunc
	storage string
}

// Hlsd is one running hls_server process.
type Hlsd struct {
	index     *tocindex.Index
	client    *hlsclient.Client
	apiServer *hlsapi.Server
	cache     *segmentcache.Cache
	seed      []hlsconfig.Channel

	configPath string

	httpSrv *http.Server

	logf func(format string, args ...any)
}

// New builds the one hlsclient.Client for cfg.Farcd, opens the disk cache,
// and wires internal/hlsapi's handler, but starts nothing yet — call Run to
// actually start serving, subscribing, and reconciling. cfg is assumed
// already validated by hlsconfig.Load. cfg.Channels is only a bootstrap
// seed (ADR-021): Run's reconcile loop converges the actually-served
// channel set to farcd's live GET /channels list, which becomes
// authoritative from the first reconciliation pass onward. configPath is
// the file cfg's Channels came from — kept so every subsequent tracked-state
// change can be written back to it (persist), the same way
// internal/farcd.New keeps its own configPath for persistNewChannel/etc.
func New(cfg *hlsconfig.Config, configPath string) (*Hlsd, error) {
	h := &Hlsd{
		index:      tocindex.NewIndex(),
		client:     hlsclient.New(cfg.Farcd.HTTP, cfg.Farcd.WS),
		seed:       cfg.Channels,
		configPath: configPath,
		logf:       func(string, ...any) {},
	}

	cache, err := newCache(cfg)
	if err != nil {
		return nil, fmt.Errorf("hlsd: %w", err)
	}
	h.cache = cache

	initial := make(map[uint16]bool, len(cfg.Channels))
	for _, cc := range cfg.Channels {
		initial[cc.ID] = true
	}
	h.apiServer = hlsapi.New(h.index, h.client, initial, h.cache, cfg.TargetSegmentDuration.Duration())
	h.httpSrv = &http.Server{Addr: cfg.HTTP.String(), Handler: h.apiServer.Handler(), ReadHeaderTimeout: readHeaderTimeout}

	return h, nil
}

// newCache builds cfg.CacheBackend's segmentcache.Cache -- "disk" (the
// quota-bounded local LRU) or "s3" (object storage shared across every
// hls_server replica, see internal/segmentcache's package doc). cfg is
// assumed already validated by hlsconfig.Load, so CacheBackend is one of
// these two values and the fields each one needs are non-empty.
func newCache(cfg *hlsconfig.Config) (*segmentcache.Cache, error) {
	if cfg.CacheBackend == "s3" {
		client, err := newS3Client(cfg)
		if err != nil {
			return nil, fmt.Errorf("build s3 client: %w", err)
		}
		return segmentcache.NewS3(client, cfg.S3Bucket), nil
	}
	return segmentcache.New(cfg.CacheDir, cfg.CacheQuotaBytes)
}

// newS3Client builds an S3 client against cfg.S3Endpoint -- any
// S3-compatible server (SeaweedFS's S3 gateway, MinIO, AWS S3, Ceph RGW,
// ...), never a specific product. UsePathStyle is required by most
// non-AWS S3-compatible servers (virtual-hosted-style bucket addressing
// needs per-bucket DNS, which self-hosted deployments don't have).
func newS3Client(cfg *hlsconfig.Config) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"), // required by the SDK, unused by S3-compatible servers
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if cfg.S3UseSSL {
		scheme = "https"
	}
	endpoint := scheme + "://" + cfg.S3Endpoint
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = &endpoint
		o.UsePathStyle = true
	}), nil
}

// SetLogger sets a callback for non-fatal diagnostics, used by this
// package's own shutdown/reconciliation logging and passed to every
// tocindex.EventSubscriber started after this call — Run's seed loop and
// reconcile's startChannel both construct subscribers only after SetLogger
// has already been called (cmd/hls_server always calls SetLogger before
// Run).
func (h *Hlsd) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h.logf = logf
}

// Run starts the reconciliation loop (which itself starts one
// tocindex.EventSubscriber per currently-known channel and keeps that set
// converged to farcd's live channel list) and the HTTP server, then blocks
// until ctx is cancelled, at which point it shuts everything down
// gracefully and returns. The listener failing to start (e.g. port already
// in use) also triggers shutdown and is returned as this call's error.
func (h *Hlsd) Run(ctx context.Context) error {
	go h.reconcile(ctx)

	errCh := make(chan error, 1)
	go func() {
		err := h.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("hlsd: http server: %w", err)
			return
		}
		errCh <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	h.shutdown() //nolint:contextcheck // deliberate: ctx is already Done() here, so shutdown builds its own fresh timeout context rather than reusing a cancelled one
	return runErr
}

func (h *Hlsd) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := h.httpSrv.Shutdown(shutdownCtx)
	if err != nil {
		h.logf("hlsd: server shutdown: %v", err)
	}
}

// reconcile owns tracked for the entire process lifetime — it is the only
// goroutine that ever touches it, by construction (the seed loop below,
// applyRemoteList's diff-and-act, and the event-consuming select in
// reconcileOnce all run sequentially, one after another, inside this same
// goroutine), so tracked needs no mutex at all. This is deliberate, not an
// oversight: internal/hlsapi.Server's channelSet and internal/tocindex.Index
// are genuinely read concurrently by per-request HTTP handler goroutines
// while this goroutine writes them, and those are exactly the two places
// that do carry a mutex.
func (h *Hlsd) reconcile(ctx context.Context) {
	tracked := make(map[uint16]*trackedSub, len(h.seed))
	for _, cc := range h.seed {
		h.startChannel(ctx, tracked, cc.ID, cc.Storage)
	}

	for {
		err := h.reconcileOnce(ctx, tracked)
		if err != nil {
			h.logf("hlsd: channel reconciliation: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconcileRetryDelay):
		}
	}
}

// reconcileOnce does a full GET /channels diff (the startup/reconnect
// catch-up — run every time a connection to the channel-lifecycle stream
// is (re)established, mirroring tocindex.EventSubscriber's own
// bootstrap-on-reconnect convention, ADR-016), then subscribes to
// channel.created/channel.removed (ADR-021) and processes them until the
// connection drops, periodically re-listing in the meantime to bound how
// stale a dropped event (EventPushServer.PublishChannelEvent's
// drop-if-full policy) can leave this process. A periodic re-list landing
// a moment before or after a live event for the same channel is benign and
// self-healing either way — startChannel/stopChannel are idempotent
// against tracked's current state.
func (h *Hlsd) reconcileOnce(ctx context.Context, tracked map[uint16]*trackedSub) error {
	err := h.applyRemoteList(ctx, tracked)
	if err != nil {
		return fmt.Errorf("initial channel list: %w", err)
	}

	events, err := h.client.Subscribe(ctx, "", []string{hlsclient.EventChannelCreated, hlsclient.EventChannelRemoved}, nil)
	if err != nil {
		return fmt.Errorf("subscribe to channel events: %w", err)
	}

	ticker := time.NewTicker(channelRecheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err := h.applyRemoteList(ctx, tracked)
			if err != nil {
				h.logf("hlsd: periodic channel list recheck: %v", err)
			}
		case ev, ok := <-events:
			if !ok {
				return nil // disconnected -- reconcile's outer loop retries after backoff
			}
			switch ev.Name {
			case hlsclient.EventChannelCreated:
				h.startChannel(ctx, tracked, ev.Channel, ev.Storage)
			case hlsclient.EventChannelRemoved:
				h.stopChannel(tracked, ev.Channel)
			}
		}
	}
}

// applyRemoteList fetches farcd's live channel list and converges tracked
// to match: anything remote but untracked (or tracked under a different
// storage) is started, anything tracked but no longer remote is stopped. A
// channel already tracked under the same storage is left alone.
func (h *Hlsd) applyRemoteList(ctx context.Context, tracked map[uint16]*trackedSub) error {
	remote, err := h.client.ListChannels(ctx)
	if err != nil {
		return err
	}
	remoteByID := make(map[uint16]string, len(remote))
	for _, c := range remote {
		remoteByID[c.Channel] = c.Storage
	}

	for id, storage := range remoteByID {
		if sub, ok := tracked[id]; !ok || sub.storage != storage {
			h.startChannel(ctx, tracked, id, storage)
		}
	}
	for id := range tracked {
		if _, ok := remoteByID[id]; !ok {
			h.stopChannel(tracked, id)
		}
	}
	return nil
}

// startChannel is idempotent against tracked's current state: a no-op if
// id is already tracked under the same storage; if tracked under a
// different storage, the old subscription is torn down first (via
// stopChannel, which also clears the stale tocindex.Index entries) before
// starting the new one.
func (h *Hlsd) startChannel(ctx context.Context, tracked map[uint16]*trackedSub, id uint16, storage string) {
	if existing, ok := tracked[id]; ok {
		if existing.storage == storage {
			return
		}
		h.stopChannel(tracked, id)
	}

	subCtx, cancel := context.WithCancel(ctx)
	sub := tocindex.NewEventSubscriber(h.client, storage, []uint16{id}, h.index)
	sub.SetLogger(h.logf)
	tracked[id] = &trackedSub{cancel: cancel, storage: storage}
	h.apiServer.AddChannel(id)
	h.persist(tracked)

	go func() {
		err := sub.Run(subCtx)
		if err != nil {
			h.logf("hlsd: event subscriber for channel %d: %v", id, err)
		}
	}()
}

// stopChannel is a no-op if id isn't tracked. Otherwise it cancels the
// subscriber's context, drops it from tracked, stops serving the channel
// over HTTP, and clears its tocindex.Index entries — without this last
// step, records from a channel's old storage (before a live removal or
// reassignment) would linger forever and could be served against the wrong
// storage, something only a full process restart used to prevent (ADR-021).
func (h *Hlsd) stopChannel(tracked map[uint16]*trackedSub, id uint16) {
	sub, ok := tracked[id]
	if !ok {
		return
	}
	sub.cancel()
	delete(tracked, id)
	h.apiServer.RemoveChannel(id)
	h.index.Remove(id)
	h.persist(tracked)
}

// persist rewrites configPath to match tracked's current channel/storage
// pairs. Called after every tracked mutation (startChannel/stopChannel) so
// the file mirrors farcd's live state and becomes a better-informed
// bootstrap seed on the next restart -- ADR-021 still applies: farcd's live
// GET /channels remains authoritative at runtime regardless of what this
// file says. Sorted by channel id for a stable, diffable file, since
// tracked is a map and would otherwise iterate in random order. A write
// failure is only logged: the in-memory tracked state, and therefore what's
// actually being served, is unaffected either way.
func (h *Hlsd) persist(tracked map[uint16]*trackedSub) {
	channels := make([]hlsconfig.Channel, 0, len(tracked))
	for id, sub := range tracked {
		channels = append(channels, hlsconfig.Channel{ID: id, Storage: sub.storage})
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	err := hlsconfig.Save(h.configPath, &hlsconfig.Config{Channels: channels})
	if err != nil {
		h.logf("hlsd: persist channel list to %s: %v", h.configPath, err)
	}
}
