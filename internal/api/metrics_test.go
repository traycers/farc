package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/storage"
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

	// No IngestManager wired into this server -- farc_rtsp_bytes_received_total
	// must still appear (nil-safe), just at 0.
	wantRTSPBytes := `farc_rtsp_bytes_received_total{storage="s1"} 0`
	if !strings.Contains(body, wantRTSPBytes) {
		t.Fatalf("body missing %q; got:\n%s", wantRTSPBytes, body)
	}

	// bytesWritten/fblocksCompleted are sourced from the same HealthMonitor
	// the /metrics endpoint reads -- an independent ground truth read
	// straight off the Unit, not a re-derivation of collectUnitMetrics'
	// own arithmetic.
	_, _, _, bytesWritten := u.Health().Stats()
	wantBytesWritten := fmt.Sprintf(`farc_storage_bytes_written_total{storage="s1"} %d`, bytesWritten)
	if !strings.Contains(body, wantBytesWritten) {
		t.Fatalf("body missing %q; got:\n%s", wantBytesWritten, body)
	}
	if bytesWritten == 0 {
		t.Fatal("bytesWritten = 0, want > 0 after writeVideoFrame (test would trivially pass otherwise)")
	}

	wantFblocksCompleted := fmt.Sprintf(`farc_fblocks_completed_total{storage="s1"} %d`, u.Health().FblocksCompleted())
	if !strings.Contains(body, wantFblocksCompleted) {
		t.Fatalf("body missing %q; got:\n%s", wantFblocksCompleted, body)
	}
	if u.Health().FblocksCompleted() == 0 {
		t.Fatal("FblocksCompleted() = 0, want > 0 after writeVideoFrame (test would trivially pass otherwise)")
	}
}

// TestHandleMetrics_PerFblockSizeGaugesOneSeriesPerCompletedFblock is
// .scratch/storage-fblocks-dashboard-v2/issues/
// 04-fblock-catalog-toc-content-sizes.md's own ## Tests requirement:
// collectUnitMetrics' per-fblock emission loop must produce exactly one
// series per completed fblock, not one per storage.
func TestHandleMetrics_PerFblockSizeGaugesOneSeriesPerCompletedFblock(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	writeVideoFrame(t, u, []uint16{1}, 1, 300, 400, "frame-b", 300, 2000)
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
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(buf)

	wantCompletions := len(u.Health().FblockSizes())
	if wantCompletions == 0 {
		t.Fatal("FblockSizes() = empty, want at least the 2 writes above recorded (test would trivially pass otherwise)")
	}
	for _, metric := range []string{"farc_fblock_catalog_size_bytes", "farc_fblock_toc_size_bytes", "farc_fblock_content_size_bytes"} {
		// prometheus/client_golang exposes labels sorted alphabetically by
		// name ("fblock" before "storage"), not declaration order.
		got := strings.Count(body, metric+`{fblock="`)
		if got != wantCompletions {
			t.Fatalf("%s series count = %d, want %d (one per completed fblock); body:\n%s", metric, got, wantCompletions, body)
		}
	}
}

// TestHandleMetrics_SurvivesFblockIndexReuse is the cyclic-storage steady
// state, not an edge case: a small N=1 storage has no free fblock left
// after its first write, so its second write must reuse index 0. Before
// HealthMonitor.fblockSizes became index-keyed, this produced two
// {storage,fblock} label-identical series in one Prometheus gather, which
// promhttp.HandlerFor's default HTTPErrorOnError turns into a 500 for the
// *entire* /metrics endpoint, not just the new panels.
func TestHandleMetrics_SurvivesFblockIndexReuse(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnitWithGeometry(t, storage.Geometry{FblockSize: 8192, N: 1, MaxChannels: 8})
	writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	// Retention is 30 days (smallParams) -- write again far enough in the
	// future that index 0 (the only fblock) is past retention and gets
	// reused, exactly like TestUnit_BeginFblockWrite_
	// PublishesFblockDeletedWhenReusingReadySlot in internal/storage.
	const nsPerDay = uint64(24 * 60 * 60 * 1_000_000_000)
	farFuture := uint64(1000) + 31*nsPerDay
	writeVideoFrame(t, u, []uint16{1}, 1, farFuture, farFuture+100, "frame-b", farFuture, farFuture)

	if got := len(u.Health().FblockSizes()); got != 1 {
		t.Fatalf("FblockSizes() = %d records, want exactly 1 (index 0 reused, not duplicated)", got)
	}

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
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body:\n%s", resp.StatusCode, buf)
	}
}

// TestHandleMetrics_RTSPBytesReceivedSumsAcrossStorageChannels covers
// collectUnitMetrics' storage filter over IngestManager.List() -- a
// separate test from TestHandleMetrics because that one wires a nil
// IngestManager, and this one needs a real channel registered against the
// storage to prove the storage-id label/filter actually matches (nonzero
// byte accumulation itself is covered independently by
// internal/ingest's TestIngestManager_List_ReportsRTSPBytesReceived).
func TestHandleMetrics_RTSPBytesReceivedSumsAcrossStorageChannels(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{{
		Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "s1",
		SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
		PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
	}})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(buf)

	// The channel never actually connects (nothing listens on 127.0.0.1:1),
	// so no packets arrive -- this proves the storage="s1" label is
	// correctly emitted for a registered channel, not that accumulation
	// happens (that's internal/ingest's job to prove).
	want := `farc_rtsp_bytes_received_total{storage="s1"} 0`
	if !strings.Contains(body, want) {
		t.Fatalf("body missing %q; got:\n%s", want, body)
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
