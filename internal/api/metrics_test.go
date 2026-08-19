package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMetrics(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(buf)

	geo := smallGeometry()
	wantTotal := fmt.Sprintf(`farc_fblocks_total{storage="s1"} %d`, geo.N)
	if !strings.Contains(body, wantTotal) {
		t.Fatalf("body missing %q; got:\n%s", wantTotal, body)
	}
	// fblock 0's bootstrap write never counts as real content -- it stays
	// Uninitialized, so only the one fcontainer just written is Ready.
	wantReady := fmt.Sprintf(`farc_fblocks_ready_total{storage="s1"} %d`, 1)
	if !strings.Contains(body, wantReady) {
		t.Fatalf("body missing %q; got:\n%s", wantReady, body)
	}
	wantWrites := `farc_writes_total{storage="s1"} 1`
	if !strings.Contains(body, wantWrites) {
		t.Fatalf("body missing %q; got:\n%s", wantWrites, body)
	}
	wantCapacity := fmt.Sprintf(`farc_channel_registry_capacity{storage="s1"} %d`, geo.MaxChannels)
	if !strings.Contains(body, wantCapacity) {
		t.Fatalf("body missing %q; got:\n%s", wantCapacity, body)
	}
	wantUsed := `farc_channel_registry_used{storage="s1"} 1`
	if !strings.Contains(body, wantUsed) {
		t.Fatalf("body missing %q; got:\n%s", wantUsed, body)
	}
}

func TestHandleMetrics_NoStorages(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(buf)
	// No registered storages -- no farc_* storage-scoped metric, but the
	// process/runtime collectors (client_golang) are always present.
	if strings.Contains(body, "farc_fblocks_total") {
		t.Fatalf("body contains farc_fblocks_total with no registered storages:\n%s", body)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Fatalf("body missing go_goroutines (runtime collector):\n%s", body)
	}
}
