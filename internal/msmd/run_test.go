package msmd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/msmconfig"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never came up", addr)
}

// TestRun_ServesArchivesAPIAndShutsDownCleanly proves Run's new
// archivesapi-backed http.Server actually runs alongside the existing
// WS-consume loop (against a real farcd, real msmclient/farcctl -- not
// fakes) and that both halves shut down cleanly on context cancellation,
// per issue "move /api/v1/archives/* to msm_server"'s design point 6.
func TestRun_ServesArchivesAPIAndShutsDownCleanly(t *testing.T) {
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
		FarcWS:     wsBase,
		FarcHTTP:   farcd.URL,
		MSMBaseURL: msmSrv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		Run(ctx, cfg, t.Logf)
	}()

	waitForServer(t, cfg.HTTP.String())

	// PUT .../ttl/ for an unknown archive proves the request actually
	// reaches archivesapi -> farcctl -> the real farcd above and comes back
	// as farcd's own 404 for an unknown storage, translated into the
	// controller API's {code,message} shape.
	req, err := http.NewRequest(http.MethodPut, "http://"+cfg.HTTP.String()+"/api/v1/archives/nope/ttl/",
		strings.NewReader(`{"ttl":5}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT ttl: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unknown archive, via a real farcd round trip)", resp.StatusCode)
	}
	var body struct {
		Code int32 `json:"code"`
	}
	err = json.NewDecoder(resp.Body).Decode(&body)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != http.StatusNotFound {
		t.Fatalf("body.Code = %d, want 404 (controller {code,message} shape)", body.Code)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of ctx cancellation")
	}
}
