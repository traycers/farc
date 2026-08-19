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
// .scratch/observability/spec.md: hls_server gets a /metrics endpoint with
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
		if strings.Contains(body, "hls_server_connected_channels 1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "hls_server_connected_channels 1") {
		t.Fatalf("body missing \"hls_server_connected_channels 1\" after seeded channel connects; got:\n%s", body)
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
	if !strings.Contains(string(buf), "hls_server_connected_channels 0") {
		t.Fatalf("body missing \"hls_server_connected_channels 0\"; got:\n%s", buf)
	}
}
