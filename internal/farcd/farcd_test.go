package farcd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/config"
	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storage"
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
	err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
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

// testConfig returns a fresh config plus the path of a real file it was
// saved to -- New's second argument, and what persistNewStorage writes back
// to when a test exercises POST /storages through a live Farcd.
func testConfig(t *testing.T, channels []config.Channel) (*config.Config, string) {
	t.Helper()
	cfg := &config.Config{
		HTTP:    config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		WS:      config.WSAddr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics: config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Storages: []config.Storage{
			{ID: "disk0", Path: newInitializedStorageImage(t)},
		},
		Channels: channels,
	}
	// HTTP/WS/Metrics are env-sourced now (internal/config's package doc) --
	// set them via t.Setenv so a config.Load(path) later in the same test
	// (e.g. reloading after a simulated restart) sees the same addresses
	// cfg above was built with, not the env-unset default of port 0.
	t.Setenv("FARC_HTTP_IP", cfg.HTTP.IP)
	t.Setenv("FARC_HTTP_PORT", strconv.Itoa(cfg.HTTP.Port))
	t.Setenv("FARC_WS_IP", cfg.WS.IP)
	t.Setenv("FARC_WS_PORT", strconv.Itoa(cfg.WS.Port))
	t.Setenv("FARC_METRICS_IP", cfg.Metrics.IP)
	t.Setenv("FARC_METRICS_PORT", strconv.Itoa(cfg.Metrics.Port))
	path := filepath.Join(t.TempDir(), "farc.config.json")
	err := config.Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return cfg, path
}

// testConfigTwoStorages is like testConfig but registers two storages
// (disk0, disk1) -- used by tests exercising a channel moving between
// storages via PUT /channels/{id}.
func testConfigTwoStorages(t *testing.T, channels []config.Channel) (*config.Config, string) {
	t.Helper()
	cfg := &config.Config{
		HTTP:    config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		WS:      config.WSAddr{IP: "127.0.0.1", Port: freePort(t)},
		Metrics: config.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Storages: []config.Storage{
			{ID: "disk0", Path: newInitializedStorageImage(t)},
			{ID: "disk1", Path: newInitializedStorageImage(t)},
		},
		Channels: channels,
	}
	t.Setenv("FARC_HTTP_IP", cfg.HTTP.IP)
	t.Setenv("FARC_HTTP_PORT", strconv.Itoa(cfg.HTTP.Port))
	t.Setenv("FARC_WS_IP", cfg.WS.IP)
	t.Setenv("FARC_WS_PORT", strconv.Itoa(cfg.WS.Port))
	t.Setenv("FARC_METRICS_IP", cfg.Metrics.IP)
	t.Setenv("FARC_METRICS_PORT", strconv.Itoa(cfg.Metrics.Port))
	path := filepath.Join(t.TempDir(), "farc.config.json")
	err := config.Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return cfg, path
}

// channelEventMsg is the subset of internal/api's pushMessage a global
// (channel-lifecycle) subscription cares about.
type channelEventMsg struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Channel uint16 `json:"channel"`
	Storage string `json:"storage"`
}

// dialGlobalEvents dials wsAddr's /events/ws and sends a global
// (Storage: "") subscribe message -- the same subscription
// internal/hlsd's reconciliation loop uses.
func dialGlobalEvents(t *testing.T, wsAddr string) *websocket.Conn {
	t.Helper()
	url := "ws://" + wsAddr + "/events/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	err = conn.WriteJSON(map[string]any{"storage": ""})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestRun_CreateChannelOverHTTP_PublishesGlobalChannelCreatedEvent(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())
	waitForServer(t, cfg.WS.String())

	conn := dialGlobalEvents(t, cfg.WS.String())
	time.Sleep(50 * time.Millisecond) // let the subscribe register before publishing

	body := map[string]any{
		"id": 7, "rtsp_url": "rtsp://127.0.0.1:1/cam", "storage": "disk0",
		"capture_policy": map[string]any{"type": "continuous"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post("http://"+cfg.HTTP.String()+"/channels", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST /channels: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg channelEventMsg
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read channel event: %v", err)
	}
	if msg.Name != "channel.created" || msg.Channel != 7 || msg.Storage != "disk0" {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestRun_RemoveChannelOverHTTP_PublishesGlobalChannelRemovedEvent(t *testing.T) {
	cfg, path := testConfig(t, []config.Channel{
		{ID: 7, RTSPURL: "rtsp://127.0.0.1:1/cam", Storage: "disk0", CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())
	waitForServer(t, cfg.WS.String())

	conn := dialGlobalEvents(t, cfg.WS.String())
	time.Sleep(50 * time.Millisecond)

	req, err := http.NewRequest(http.MethodDelete, "http://"+cfg.HTTP.String()+"/channels/7", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /channels/7: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg channelEventMsg
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read channel event: %v", err)
	}
	if msg.Name != "channel.removed" || msg.Channel != 7 || msg.Storage != "disk0" {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestRun_UpdateChannelOverHTTP_StorageChanged_PublishesRemovedThenCreated(t *testing.T) {
	cfg, path := testConfigTwoStorages(t, []config.Channel{
		{ID: 7, RTSPURL: "rtsp://127.0.0.1:1/cam", Storage: "disk0", CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())
	waitForServer(t, cfg.WS.String())

	conn := dialGlobalEvents(t, cfg.WS.String())
	time.Sleep(50 * time.Millisecond)

	body := map[string]any{
		"rtsp_url": "rtsp://127.0.0.1:1/cam", "storage": "disk1",
		"capture_policy": map[string]any{"type": "continuous"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+cfg.HTTP.String()+"/channels/7", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /channels/7: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, respBody)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var removed, created channelEventMsg
	err = conn.ReadJSON(&removed)
	if err != nil {
		t.Fatalf("read first channel event: %v", err)
	}
	err = conn.ReadJSON(&created)
	if err != nil {
		t.Fatalf("read second channel event: %v", err)
	}
	if removed.Name != "channel.removed" || removed.Channel != 7 || removed.Storage != "disk0" {
		t.Fatalf("first event = %+v, want channel.removed for disk0", removed)
	}
	if created.Name != "channel.created" || created.Channel != 7 || created.Storage != "disk1" {
		t.Fatalf("second event = %+v, want channel.created for disk1", created)
	}
}

// TestRun_UpdateChannelOverHTTP_StorageUnchanged_PublishesNoGlobalEvent
// regression-tests the "don't churn hlsd's index for a no-op storage
// change" behavior: a PUT that only edits rtsp_url/capture_policy, leaving
// storage the same, must not publish anything.
func TestRun_UpdateChannelOverHTTP_StorageUnchanged_PublishesNoGlobalEvent(t *testing.T) {
	cfg, path := testConfig(t, []config.Channel{
		{ID: 7, RTSPURL: "rtsp://127.0.0.1:1/cam", Storage: "disk0", CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())
	waitForServer(t, cfg.WS.String())

	conn := dialGlobalEvents(t, cfg.WS.String())
	time.Sleep(50 * time.Millisecond)

	body := map[string]any{
		"rtsp_url": "rtsp://127.0.0.1:1/cam-edited", "storage": "disk0",
		"capture_policy": map[string]any{"type": "continuous"},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+cfg.HTTP.String()+"/channels/7", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /channels/7: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, respBody)
	}

	// ReplaceChannel always restarts the channel's ChannelIngest under the
	// hood (handleUpdateChannel's own doc comment), so a legitimate
	// channel.rtsp.connect_failed/connected/disconnected can still arrive
	// here for the freshly-restarted ingest -- this test's actual concern
	// (per its own doc comment) is specifically that a same-storage update
	// must not churn hlsd's index via channel.removed/channel.created,
	// not that zero events of any kind occur.
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		conn.SetReadDeadline(deadline)
		var msg channelEventMsg
		if err := conn.ReadJSON(&msg); err != nil {
			return // deadline hit, nothing churned -- test passes
		}
		if msg.Name == "channel.removed" || msg.Name == "channel.created" {
			t.Fatalf("unexpectedly received a channel-lifecycle event for a same-storage update: %+v", msg)
		}
	}
}

func TestNew_OpensStorageAndBuildsChannelConfig(t *testing.T) {
	cfg, path := testConfig(t, []config.Channel{
		{
			ID: 1, RTSPURL: "rtsp://127.0.0.1:1/x", Storage: "disk0",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous, MaxDeferredStart: config.Duration(30 * time.Second)},
		},
		{
			ID: 2, RTSPURL: "rtsp://127.0.0.1:1/y", Storage: "disk0",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyEvent, Prerecord: config.Duration(10 * time.Second), Postrecord: config.Duration(20 * time.Second)},
		},
	})

	f, err := New(cfg, path)
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

func TestNew_OpensEachStorageWithItsOwnConfiguredPoolTuning(t *testing.T) {
	cfg, path := testConfigTwoStorages(t, nil)
	cfg.Storages[0].PoolSize, cfg.Storages[0].PoolWarningAt, cfg.Storages[0].PoolBackpressureAt = 8, 4, 8
	// disk1 left at zero -- must resolve to storage.DefaultPoolTuning(), not
	// inherit disk0's explicit values.
	err := config.Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.closeUnits()

	disk0, ok := f.registry.Get("disk0")
	if !ok {
		t.Fatalf("disk0 not registered")
	}
	if got := disk0.PoolTuning(); got != (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("disk0 PoolTuning() = %+v, want {8 4 8}", got)
	}

	disk1, ok := f.registry.Get("disk1")
	if !ok {
		t.Fatalf("disk1 not registered")
	}
	if got := disk1.PoolTuning(); got != storage.DefaultPoolTuning() {
		t.Fatalf("disk1 PoolTuning() = %+v, want defaults %+v", got, storage.DefaultPoolTuning())
	}
}

func TestNew_UnknownStorageReferenceCleansUpAndErrors(t *testing.T) {
	cfg, path := testConfig(t, []config.Channel{
		{ID: 1, RTSPURL: "rtsp://127.0.0.1:1/x", Storage: "nope",
			CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})

	_, err := New(cfg, path)
	if err == nil {
		t.Fatalf("New: want error for unknown storage reference, got nil")
	}
}

func TestRun_ServesAndShutsDownGracefully(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
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
	err = json.Unmarshal(body, &list)
	if err != nil {
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

// TestRun_HTTPMetrics_ReachableOnMetricsEndpoint guards the wiring in New
// (httpMetrics := tracing.NewHTTPMetrics(apiServer.Registerer())): it's easy
// to build tracing.HTTPMetrics on a registry that never backs f.metricsSrv,
// leaving it silently unreachable on scrape even though every
// internal/tracing unit test passes. Drives a real request through
// f.httpSrv, then asserts the resulting metrics appear on f.metricsSrv's own
// /metrics -- not a registry this test constructs itself.
func TestRun_HTTPMetrics_ReachableOnMetricsEndpoint(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	defer cancel()

	waitForServer(t, cfg.HTTP.String())

	resp, err := http.Get("http://" + cfg.HTTP.String() + "/storages")
	if err != nil {
		t.Fatalf("GET /storages: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /storages status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		metricsResp, err := http.Get("http://" + cfg.Metrics.String() + "/metrics")
		if err != nil {
			t.Fatalf("GET /metrics: %v", err)
		}
		b, _ := io.ReadAll(metricsResp.Body)
		metricsResp.Body.Close()
		body = string(b)
		if strings.Contains(body, `http_requests_total{code="200",method="GET",pattern="GET /storages"} 1`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, `http_requests_total{code="200",method="GET",pattern="GET /storages"} 1`) {
		t.Fatalf("body missing the GET /storages http_requests_total sample; got:\n%s", body)
	}
}

func TestRun_CreateStorageOverHTTP_PersistsToConfigFile(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	imgPath := filepath.Join(t.TempDir(), "new.img")
	catalogPath := filepath.Join(t.TempDir(), "new0.catalog")
	body := map[string]any{
		"id":   "new0",
		"path": imgPath,
		"geometry": map[string]any{
			"FblockSize": smallGeometry().FblockSize, "N": smallGeometry().N, "MaxChannels": smallGeometry().MaxChannels,
		},
		"params": map[string]any{
			"fchunk_size": 1024, "write_mode": "cyclic",
			"retention": map[string]any{"days": 30}, "min_container_share": 0.1,
		},
		"catalog_path": catalogPath,
		"backend":      "standard",
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post("http://"+cfg.HTTP.String()+"/storages", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST /storages: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, respBody)
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

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if len(reloaded.Storages) != 2 {
		t.Fatalf("Storages after restart = %+v, want disk0 + new0", reloaded.Storages)
	}
	var found bool
	for _, s := range reloaded.Storages {
		if s.ID == "new0" {
			found = true
			if s.Path != imgPath || s.CatalogPath != catalogPath {
				t.Fatalf("new0 entry = %+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("new0 not present after restart: %+v", reloaded.Storages)
	}
}

func TestRun_PatchStoragePoolOverHTTP_PersistsToConfigFile(t *testing.T) {
	cfg, path := testConfig(t, nil) // disk0 already registered
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	body := map[string]any{"pool": map[string]any{"Size": 8, "WarningAt": 4, "BackpressureAt": 8}}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, "http://"+cfg.HTTP.String()+"/storages/disk0", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /storages/disk0: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", resp.StatusCode, respBody)
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

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	var found bool
	for _, s := range reloaded.Storages {
		if s.ID == "disk0" {
			found = true
			if s.PoolSize != 8 || s.PoolWarningAt != 4 || s.PoolBackpressureAt != 8 {
				t.Fatalf("disk0 pool config = %+v, want 8/4/8", s)
			}
		}
	}
	if !found {
		t.Fatalf("disk0 not present after restart: %+v", reloaded.Storages)
	}
}

func TestRun_CreateChannelOverHTTP_PersistsToConfigFile(t *testing.T) {
	cfg, path := testConfig(t, nil) // disk0 already registered
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- f.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	body := map[string]any{
		"id":       7,
		"rtsp_url": "rtsp://127.0.0.1:1/cam",
		"storage":  "disk0",
		"capture_policy": map[string]any{
			"type": "event", "prerecord_ns": 5_000_000_000, "postrecord_ns": 10_000_000_000,
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post("http://"+cfg.HTTP.String()+"/channels", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST /channels: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, respBody)
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

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	var found bool
	for _, c := range reloaded.Channels {
		if c.ID == 7 {
			found = true
			if c.RTSPURL != "rtsp://127.0.0.1:1/cam" || c.Storage != "disk0" ||
				c.CapturePolicy.Type != config.CapturePolicyEvent ||
				c.CapturePolicy.Prerecord.Duration() != 5*time.Second ||
				c.CapturePolicy.Postrecord.Duration() != 10*time.Second {
				t.Fatalf("channel 7 = %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("channel 7 not present after restart: %+v", reloaded.Channels)
	}

	// And a fresh New() against the reloaded config should actually build
	// and register the channel -- the real point of persisting it at all.
	f2, err := New(reloaded, path)
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}
	defer f2.closeUnits()
	if len(f2.channels) != 1 || f2.channels[0].Channel != 7 {
		t.Fatalf("channels after restart = %+v", f2.channels)
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

// TestFarcd_PersistNewStorage_AlreadyPresentSkipsSave guards
// withConfigMutation's idempotent no-op path: if the mutate closure reports
// nothing to do, config.Save must never be attempted at all -- proven here
// deterministically by pointing configPath at a directory that doesn't
// exist (Save would fail if it were ever called).
func TestFarcd_PersistNewStorage_AlreadyPresentSkipsSave(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.closeUnits()

	f.configPath = filepath.Join(t.TempDir(), "does-not-exist", "farc.config.json")
	err = f.persistNewStorage("disk0", "irrelevant", "", "irrelevant", storage.PoolTuning{}) // disk0 is already in cfg.Storages
	if err != nil {
		t.Fatalf("persistNewStorage (already present) = %v, want nil (idempotent, Save never attempted)", err)
	}
}

// TestFarcd_PersistNewStorage_RollsBackOnSaveFailure guards
// withConfigMutation's rollback path: a real mutation whose Save fails must
// leave f.cfg exactly as it was before the call.
func TestFarcd_PersistNewStorage_RollsBackOnSaveFailure(t *testing.T) {
	cfg, path := testConfig(t, nil)
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.closeUnits()

	before := len(f.cfg.Storages)
	f.configPath = filepath.Join(t.TempDir(), "does-not-exist", "farc.config.json")
	err = f.persistNewStorage("disk1", "/tmp/disk1.img", "", "Disk 1", storage.PoolTuning{})
	if err == nil {
		t.Fatal("persistNewStorage = nil error, want the Save failure to surface")
	}
	if len(f.cfg.Storages) != before {
		t.Fatalf("f.cfg.Storages after failed persist = %+v, want rolled back to the original %d entries", f.cfg.Storages, before)
	}
}
