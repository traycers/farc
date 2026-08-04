package farcd

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/config"
	"traycers/farc/internal/ingest"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
)

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

// newInitializedStorageImage creates and initializes a fresh Storage image
// under t.TempDir(), leaving it closed and ready for farcd.New to Open.
func newInitializedStorageImage(t *testing.T) string {
	t.Helper()
	imgPath := filepath.Join(t.TempDir(), "storage.img")
	geo := smallGeometry()
	if err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644); err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	if err := storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1}); err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close after init: %v", err)
	}
	return imgPath
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func testConfig(t *testing.T, channels []config.Channel) *config.Config {
	t.Helper()
	return &config.Config{
		HTTP:    config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		WS:      config.WSAddr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics: config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Storages: []config.Storage{
			{ID: "disk0", Path: newInitializedStorageImage(t)},
		},
		Channels: channels,
	}
}

func TestNew_OpensStorageAndBuildsChannelConfig(t *testing.T) {
	cfg := testConfig(t, []config.Channel{
		{
			ID: 1, RTSPURL: "rtsp://127.0.0.1:1/x", Storage: "disk0",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous, MaxDeferredStart: config.Duration(30 * time.Second)},
		},
		{
			ID: 2, RTSPURL: "rtsp://127.0.0.1:1/y", Storage: "disk0",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyEvent, Prerecord: config.Duration(10 * time.Second), Postrecord: config.Duration(20 * time.Second)},
		},
	})

	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.closeUnits()

	if len(f.units) != 1 {
		t.Fatalf("units = %d, want 1", len(f.units))
	}
	if _, ok := f.registry.Get("disk0"); !ok {
		t.Fatalf("storage disk0 not registered")
	}
	if len(f.channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(f.channels))
	}

	c1 := f.channels[0]
	if c1.Channel != 1 || c1.PolicyType != ingest.PolicyContinuous || c1.QueueDepth != uint64(30*time.Second) {
		t.Fatalf("channel 1 = %+v", c1)
	}
	c2 := f.channels[1]
	if c2.Channel != 2 || c2.PolicyType != ingest.PolicyEvent || c2.QueueDepth != uint64(10*time.Second) ||
		c2.PolicyParams.Prerecord != uint64(10*time.Second) || c2.PolicyParams.Postrecord != uint64(20*time.Second) {
		t.Fatalf("channel 2 = %+v", c2)
	}
	if c1.BackpressureSignal == nil || c1.BackpressureSignal() {
		t.Fatalf("channel 1 BackpressureSignal should be non-nil and false (no writes yet)")
	}
}

func TestNew_UnknownStorageReferenceCleansUpAndErrors(t *testing.T) {
	cfg := testConfig(t, []config.Channel{
		{ID: 1, RTSPURL: "rtsp://127.0.0.1:1/x", Storage: "nope",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})

	_, err := New(cfg)
	if err == nil {
		t.Fatalf("New: want error for unknown storage reference, got nil")
	}
}

func TestRun_ServesAndShutsDownGracefully(t *testing.T) {
	cfg := testConfig(t, nil)
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()

	waitForServer(t, cfg.HTTP.String())

	resp, err := http.Get("http://" + cfg.HTTP.String() + "/storages")
	if err != nil {
		t.Fatalf("GET /storages: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(list) != 1 || list[0]["id"] != "disk0" {
		t.Fatalf("storages list = %s", body)
	}

	metricsResp, err := http.Get("http://" + cfg.Metrics.String() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", metricsResp.StatusCode)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
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
