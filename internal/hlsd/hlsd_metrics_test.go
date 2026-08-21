package hlsd_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/traycers/farc/internal/hlsconfig"
	"github.com/traycers/farc/internal/hlsd"
)

// TestRun_MetricsEndpoint_ReportsConnectedChannels is the regression test for
// .scratch/observability/spec.md: hlsd gets a /metrics endpoint with
// free client_golang runtime metrics plus one domain gauge, the count of
// currently connected/tracked channels.
func TestRun_MetricsEndpoint_ReportsConnectedChannels(t *testing.T) {
	unit := newTestUnit(t)
	farcd := newFarcdTestServer(t, unit, 1)

	cfg := &hlsconfig.Config{
		HTTP:    hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics: hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:   hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		Channels: []hlsconfig.Channel{
			{ID: 1, Storage: "s1"},
		},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}

	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	waitForServer(t, cfg.Metrics.String())

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		_, buf := mustGet(t, "http://"+cfg.Metrics.String()+"/metrics")
		body = string(buf)
		if strings.Contains(body, "hlsd_connected_channels 1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "hlsd_connected_channels 1") {
		t.Fatalf("body missing \"hlsd_connected_channels 1\" after seeded channel connects; got:\n%s", body)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Fatalf("body missing go_goroutines (runtime collector); got:\n%s", body)
	}
}

// TestRun_MetricsEndpoint_NoChannels asserts the gauge reads 0 with nothing
// tracked, rather than being silently absent.
func TestRun_MetricsEndpoint_NoChannels(t *testing.T) {
	unit := newTestUnit(t)
	farcd := newFarcdTestServer(t, unit)

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics:               hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}

	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	waitForServer(t, cfg.Metrics.String())

	resp, buf := mustGet(t, "http://"+cfg.Metrics.String()+"/metrics")
	if resp != http.StatusOK {
		t.Fatalf("GET /metrics status = %d", resp)
	}
	if !strings.Contains(string(buf), "hlsd_connected_channels 0") {
		t.Fatalf("body missing \"hlsd_connected_channels 0\"; got:\n%s", buf)
	}
}

// TestRun_HTTPMetrics_ReachableOnMetricsEndpoint is the reachability
// regression this needs: tracing.HTTPMetrics is built on its own registry in
// internal/tracing, and it's easy to wire it onto a registry that never
// backs the real /metrics handler, leaving the panels this feeds silently
// empty even though every unit test in internal/tracing passes. This drives
// a real HTTP request through h.httpSrv, then asserts the resulting metrics
// actually appear in h.metricsSrv's own /metrics response -- not a registry
// this test constructs itself.
func TestRun_HTTPMetrics_ReachableOnMetricsEndpoint(t *testing.T) {
	unit := newTestUnit(t)
	farcd := newFarcdTestServer(t, unit)

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics:               hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}

	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()

	waitForServer(t, cfg.HTTP.String())
	waitForServer(t, cfg.Metrics.String())

	// GET /timeline with no query params 400s fast (parseChannelList fails
	// first) -- the point isn't the response, it's that Middleware recorded
	// it under the matched route pattern, not the raw path.
	status, _ := mustGet(t, "http://"+cfg.HTTP.String()+"/timeline")
	if status != http.StatusBadRequest {
		t.Fatalf("GET /timeline status = %d, want %d", status, http.StatusBadRequest)
	}

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		_, buf := mustGet(t, "http://"+cfg.Metrics.String()+"/metrics")
		body = string(buf)
		if strings.Contains(body, `http_requests_total{code="400",method="GET",pattern="GET /timeline"} 1`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `http_requests_total{code="400",method="GET",pattern="GET /timeline"} 1`) {
		t.Fatalf("body missing the GET /timeline http_requests_total sample; got:\n%s", body)
	}
	if !strings.Contains(body, `http_request_duration_seconds_bucket{method="GET",pattern="GET /timeline",le="0.3"}`) {
		t.Fatalf("body missing an explicit le=\"0.3\" histogram bucket for GET /timeline; got:\n%s", body)
	}
}
