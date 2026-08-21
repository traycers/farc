package apid

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/traycers/farc/internal/apidconfig"
	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/internal/tracing"
)

// shutdownTimeout bounds how long graceful HTTP shutdown waits for
// in-flight requests before Run returns anyway -- matches
// internal/farcd's/internal/hlsd's own constant.
const shutdownTimeout = 10 * time.Second

// readHeaderTimeout bounds how long the http.Server waits for a client to
// finish sending request headers -- matches internal/farcd's/
// internal/hlsd's own constant.
const readHeaderTimeout = 10 * time.Second

// Apid is one running apid process.
type Apid struct {
	httpSrv    *http.Server
	metricsSrv *http.Server

	logf func(format string, args ...any)
}

// New builds apid's farcd/mediamtx clients, orchestrator, and HTTP/metrics
// servers, but starts nothing yet -- call Run to actually start serving.
// cfg is assumed already validated by apidconfig.Load.
func New(cfg *apidconfig.Config) *Apid {
	a := &Apid{logf: func(string, ...any) {}}

	orch := NewOrchestrator(
		NewFarcdClient(cfg.Farcd.HTTP),
		NewMediamtxClient(cfg.Mediamtx.APIBase),
		cfg.Mediamtx.RTSPBase,
		cfg.WebRTCPublicBase,
	)
	srv := NewServer(orch)

	trace := func(format string, args ...any) { a.logf(format, args...) }
	metricsReg := newMetricsRegistry()
	httpMetrics := tracing.NewHTTPMetrics(metricsReg)
	a.httpSrv = &http.Server{
		Addr:              cfg.HTTP.String(),
		Handler:           tracing.Middleware(trace, httpMetrics)(srv.Handler()),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	// metricsSrv is left unwrapped -- internal scrape traffic, matching
	// internal/farcd's/internal/hlsd's identical convention.
	a.metricsSrv = &http.Server{Addr: cfg.Metrics.String(), Handler: newMetricsHandler(metricsReg), ReadHeaderTimeout: readHeaderTimeout}

	return a
}

// SetLogger sets a callback for non-fatal diagnostics (access logging,
// shutdown errors).
func (a *Apid) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	a.logf = logf
}

// Run starts the HTTP and metrics servers, then blocks until ctx is
// cancelled, at which point it shuts both down gracefully and returns. A
// listener failing to start (e.g. port already in use) also triggers
// shutdown and is returned as this call's error.
func (a *Apid) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() {
		err := a.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("apid: http server: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		err := a.metricsSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("apid: metrics server: %w", err)
			return
		}
		errCh <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	a.shutdown() //nolint:contextcheck // deliberate: ctx is already Done() here, so shutdown builds its own fresh timeout context rather than reusing a cancelled one
	return runErr
}

func (a *Apid) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, srv := range []*http.Server{a.httpSrv, a.metricsSrv} {
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			levellog.New(a.logf).Error("apid: server shutdown: %v", err)
		}
	}
}
