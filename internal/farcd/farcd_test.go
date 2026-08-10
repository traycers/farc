package farcd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"traycers/farc/fblock"
	"traycers/farc/internal/config"
	"traycers/farc/internal/ingest"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
)

// unknownUUIDHex is a UUID guaranteed not to resolve (see internal/api's
// identically-named test constant for why not all-zero).
const unknownUUIDHex = "ffffffffffffffffffffffffffffffff"

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
// regression-tests the "don't churn hls_server's index for a no-op storage
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

	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var msg channelEventMsg
	if err := conn.ReadJSON(&msg); err == nil {
		t.Fatalf("unexpectedly received a channel event for a same-storage update: %+v", msg)
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

// TestFarcd_LiveProgress_TracksRecordingChannelLifecycle exercises
// SetOnRecordingChange's liveCursors bookkeeping and tickLiveProgress's
// guard logic directly (white-box, no HTTP/WS needed) -- the actual tree
// data these feed into fblock-live's WS push is unit-tested at the
// ingest/api layers (CapturePolicy.LiveElementsSince, EventPushServer.
// PublishLiveProgress); this test only checks farcd's own wiring between
// them doesn't panic and tracks the right channel at the right times.
func TestFarcd_LiveProgress_TracksRecordingChannelLifecycle(t *testing.T) {
	cfg, path := testConfig(t, []config.Channel{
		{ID: 7, RTSPURL: "rtsp://127.0.0.1:1/cam", Storage: "disk0", CapturePolicy: config.CapturePolicy{Type: config.CapturePolicyContinuous}},
	})
	f, err := New(cfg, path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.closeUnits()

	f.ing.Start(f.channels)
	defer f.ing.Stop()

	if _, _, _, ok := f.ing.LiveElementsSince(7, 0); ok {
		t.Fatal("LiveElementsSince before recording starts: want ok=false")
	}

	err = f.ing.StartRecording(7, 1000, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	f.liveMu.Lock()
	_, tracked := f.liveCursors[7]
	f.liveMu.Unlock()
	if !tracked {
		t.Fatal("liveCursors has no entry for channel 7 right after recording starts")
	}

	// An empty segment (no frames yet) must tick without panicking and
	// without publishing anything (tickLiveProgress skips zero-length
	// deltas) -- there's no subscriber here, so a wrongly-attempted publish
	// would just be silently dropped rather than failing this test, but a
	// panic (e.g. a nil map deref) would not be.
	f.tickLiveProgress()

	err = f.ing.StopRecording(7, 2000)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	f.liveMu.Lock()
	_, tracked = f.liveCursors[7]
	f.liveMu.Unlock()
	if tracked {
		t.Fatal("liveCursors still has an entry for channel 7 after recording stops")
	}
	if _, _, _, ok := f.ing.LiveElementsSince(7, 0); ok {
		t.Fatal("LiveElementsSince after recording stops: want ok=false")
	}
}

// TestRun_ServesFblockTreeAndLiveProgress is an end-to-end smoke test
// against a fully running Farcd (real net/http + gorilla/websocket servers,
// not internal/api's httptest-only coverage): GET .../tree reaches the
// actual HTTP route registered in server.go, and a WS client subscribed to
// a recording channel's Channels stays connected across a live-progress
// tick without the ticker goroutine panicking. It doesn't write a real
// fblock through farcd's own (O_DIRECT) storage backend -- that write path
// requires block-device-grade alignment this package's other tests never
// exercise either, since they only ever read/administer "disk0" -- decoding
// a finalized tree's actual content is already covered at the internal/api
// layer (TestHandleReadTree_WalksVideoFrame) against a Storage opened via
// the alignment-free "standard" backend.
func TestRun_ServesFblockTreeAndLiveProgress(t *testing.T) {
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

	resp, err := http.Get(fmt.Sprintf("http://%s/storages/disk0/fcontainers/%s/tree", cfg.HTTP.String(), unknownUUIDHex))
	if err != nil {
		t.Fatalf("GET .../tree: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a uuid that was never written", resp.StatusCode)
	}

	wsURL := "ws://" + cfg.WS.String() + "/events/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer conn.Close()
	err = conn.WriteJSON(map[string]any{"storage": "", "want": []string{}, "channels": []int{7}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	startResp, err := http.Post(fmt.Sprintf("http://%s/channels/7/recording/start", cfg.HTTP.String()), "application/json", nil)
	if err != nil {
		t.Fatalf("POST recording/start: %v", err)
	}
	startResp.Body.Close()

	// Outlive at least one liveProgressInterval tick with the WS connection
	// held open, then confirm it's still alive -- a panic in
	// runLiveProgressTicker or PublishLiveProgress would have taken the
	// whole process down well before this point. The server never sends a
	// second message on a channel with no real frames (nothing to report),
	// so a read here is expected to time out, not to return data or a
	// close; anything else means the tick broke the connection.
	time.Sleep(liveProgressInterval + 200*time.Millisecond)
	err = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, _, err = conn.ReadMessage()
	var netErr net.Error
	if err != nil && (!errors.As(err, &netErr) || !netErr.Timeout()) {
		t.Fatalf("WS connection unexpectedly closed after a live-progress tick: %v", err)
	}

	select {
	case err := <-runErr:
		t.Fatalf("Run returned early: %v", err)
	default:
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
