// Package hlsd is hls_server's process wiring, mirroring internal/farcd's
// own New/SetLogger/Run/shutdown orchestrator shape: load hlsconfig ->
// build one hlsclient.Client per configured farcd endpoint -> open the
// disk segment cache -> start one tocindex.EventSubscriber per configured
// channel (ADR-018) -> serve internal/hlsapi on one listener -> graceful
// shutdown on context cancellation.
package hlsd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

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

// Hlsd is one running hls_server process.
type Hlsd struct {
	index *tocindex.Index
	subs  []*tocindex.EventSubscriber
	cache *segmentcache.Cache

	httpSrv *http.Server

	logf func(format string, args ...any)
}

// New builds every hlsclient.Client, opens the disk cache, and wires
// internal/hlsapi's handler, but starts nothing yet — call Run to actually
// start serving and subscribing. cfg is assumed already validated by
// hlsconfig.Load (in particular, every cc.Farcd is guaranteed present in
// cfg.Farcds).
func New(cfg *hlsconfig.Config) (*Hlsd, error) {
	h := &Hlsd{
		index: tocindex.NewIndex(),
		logf:  func(string, ...any) {},
	}

	clientsByFarcd := make(map[string]*hlsclient.Client, len(cfg.Farcds))
	for _, f := range cfg.Farcds {
		clientsByFarcd[f.ID] = hlsclient.New(f.HTTP, f.WS)
	}

	cache, err := segmentcache.New(cfg.CacheDir, cfg.CacheQuotaBytes)
	if err != nil {
		return nil, fmt.Errorf("hlsd: %w", err)
	}
	h.cache = cache

	clientsByChannel := make(map[uint16]*hlsclient.Client, len(cfg.Channels))
	for _, cc := range cfg.Channels {
		client := clientsByFarcd[cc.Farcd]
		clientsByChannel[cc.ID] = client
		h.subs = append(h.subs, tocindex.NewEventSubscriber(client, cc.Storage, []uint16{cc.ID}, h.index))
	}

	apiServer := hlsapi.New(h.index, clientsByChannel, h.cache, cfg.TargetSegmentDuration.Duration())
	h.httpSrv = &http.Server{Addr: cfg.HTTP.String(), Handler: apiServer.Handler()}

	return h, nil
}

// SetLogger sets a callback for non-fatal diagnostics, forwarded to every
// EventSubscriber and used for this package's own shutdown logging.
func (h *Hlsd) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h.logf = logf
	for _, s := range h.subs {
		s.SetLogger(logf)
	}
}

// Run starts every EventSubscriber and the HTTP server, then blocks until
// ctx is cancelled, at which point it shuts everything down gracefully and
// returns. The listener failing to start (e.g. port already in use) also
// triggers shutdown and is returned as this call's error.
func (h *Hlsd) Run(ctx context.Context) error {
	for _, s := range h.subs {
		go func(s *tocindex.EventSubscriber) {
			if err := s.Run(ctx); err != nil {
				h.logf("hlsd: event subscriber: %v", err)
			}
		}(s)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := h.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	h.shutdown()
	return runErr
}

func (h *Hlsd) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := h.httpSrv.Shutdown(shutdownCtx); err != nil {
		h.logf("hlsd: server shutdown: %v", err)
	}
}
