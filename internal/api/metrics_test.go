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
	if err := reg.Register("s1", u, "s1.img"); err != nil {
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
	// fblock 0 (Storage's own bootstrap header) plus the one just written
	// -- 2 Ready, N-2 uninitialized, 0 bad/in_progress.
	wantReady := fmt.Sprintf(`farc_fblocks_ready_total{storage="s1"} %d`, 2)
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
	if len(buf) != 0 {
		t.Fatalf("body = %q, want empty (no registered storages)", buf)
	}
}
