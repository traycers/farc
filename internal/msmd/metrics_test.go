package msmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/msmconfig"
)

// TestRun_MetricsEndpoint_ReportsWSConnectionStatus is the regression test
// for .scratch/observability/spec.md: msm_server gets a /metrics endpoint
// with free client_golang runtime metrics plus one domain gauge, whether the
// outbound WS subscription to farcd's event feed is currently connected.
func TestRun_MetricsEndpoint_ReportsWSConnectionStatus(t *testing.T) {
	reg := api.NewStorageRegistry()
	push := api.NewEventPushServer(reg)
	apiSrv := api.NewHttpApiServer(reg, nil, push)
	farcd := httptest.NewServer(apiSrv.Handler())
	defer farcd.Close()
	wsBase := "ws" + strings.TrimPrefix(farcd.URL, "http")

	msmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer msmSrv.Close()

	cfg := &msmconfig.Config{
		HTTP:       msmconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics:    msmconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		FarcWS:     wsBase,
		FarcHTTP:   farcd.URL,
		MSMBaseURL: msmSrv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, cfg, t.Logf)

	waitForServer(t, cfg.Metrics.String())

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + cfg.Metrics.String() + "/metrics")
		if err != nil {
			t.Fatalf("GET /metrics: %v", err)
		}
		buf, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(buf)
		if strings.Contains(body, "msm_server_ws_connected 1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "msm_server_ws_connected 1") {
		t.Fatalf("body missing \"msm_server_ws_connected 1\" after connecting to farcd; got:\n%s", body)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Fatalf("body missing go_goroutines (runtime collector); got:\n%s", body)
	}
}
