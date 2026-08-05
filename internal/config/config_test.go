package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setRequiredEnv sets the env vars loadEnv treats as required (the three
// *_PORT variables) to the values docs/docs/archive/04-storage-operations.md
// §2.1's worked example uses, via t.Setenv so each test gets its own,
// automatically-restored environment.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("FARC_HTTP_PORT", "8080")
	t.Setenv("FARC_WS_PORT", "8081")
	t.Setenv("FARC_WS_MAX_CONNECTIONS", "100")
	t.Setenv("FARC_METRICS_PORT", "9090")
}

// docExample is docs/docs/archive/04-storage-operations.md §2.1's own
// worked JSON example, minus the http/ws/metrics section — those now come
// from env (setRequiredEnv), not the JSON file.
const docExample = `{
  "storages": [
    {
      "id": "disk0",
      "path": "/dev/sdb1",
      "catalog_path": "/mnt/ssd/disk0.catalog"
    }
  ],
  "channels": [
    {
      "id": 42,
      "rtsp_url": "rtsp://camera1/stream",
      "storage": "disk0",
      "capture_policy": { "type": "continuous", "max_deferred_start": "30s" }
    },
    {
      "id": 43,
      "rtsp_url": "rtsp://camera2/stream",
      "storage": "disk0",
      "capture_policy": { "type": "event", "prerecord": "10s", "postrecord": "30s" }
    }
  ]
}`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "farc.config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad_DocExample(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load(writeConfig(t, docExample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.String() != "0.0.0.0:8080" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
	if cfg.WS.String() != "0.0.0.0:8081" || cfg.WS.MaxConnections != 100 {
		t.Fatalf("WS = %+v", cfg.WS)
	}
	if cfg.Metrics.String() != "0.0.0.0:9090" {
		t.Fatalf("Metrics = %s", cfg.Metrics)
	}
	if len(cfg.Storages) != 1 || cfg.Storages[0].ID != "disk0" || cfg.Storages[0].Path != "/dev/sdb1" || cfg.Storages[0].CatalogPath != "/mnt/ssd/disk0.catalog" {
		t.Fatalf("Storages = %+v", cfg.Storages)
	}
	if len(cfg.Channels) != 2 {
		t.Fatalf("Channels = %+v", cfg.Channels)
	}

	ch42 := cfg.Channels[0]
	if ch42.ID != 42 || ch42.RTSPURL != "rtsp://camera1/stream" || ch42.Storage != "disk0" {
		t.Fatalf("channel 42 = %+v", ch42)
	}
	if ch42.CapturePolicy.Type != CapturePolicyContinuous || ch42.CapturePolicy.MaxDeferredStart.Duration() != 30*time.Second {
		t.Fatalf("channel 42 capture_policy = %+v", ch42.CapturePolicy)
	}

	ch43 := cfg.Channels[1]
	if ch43.CapturePolicy.Type != CapturePolicyEvent ||
		ch43.CapturePolicy.Prerecord.Duration() != 10*time.Second ||
		ch43.CapturePolicy.Postrecord.Duration() != 30*time.Second {
		t.Fatalf("channel 43 capture_policy = %+v", ch43.CapturePolicy)
	}
}

func TestLoad_CustomEnvAddr(t *testing.T) {
	t.Setenv("FARC_HTTP_IP", "127.0.0.1")
	t.Setenv("FARC_HTTP_PORT", "18080")
	t.Setenv("FARC_WS_PORT", "18081")
	t.Setenv("FARC_METRICS_PORT", "19090")

	const doc = `{"storages": [{"id":"disk0","path":"/dev/sdb1"}], "channels": []}`
	cfg, err := Load(writeConfig(t, doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.String() != "127.0.0.1:18080" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
}

func TestLoad_MissingPortEnvRejected(t *testing.T) {
	const doc = `{"storages": [{"id":"disk0","path":"/dev/sdb1"}], "channels": []}`
	// No env vars set at all -- every *_PORT defaults to 0.
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for missing FARC_HTTP_PORT, got nil")
	}
}

func TestLoad_InvalidPortEnvRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("FARC_HTTP_PORT", "not-a-number")

	const doc = `{"storages": [{"id":"disk0","path":"/dev/sdb1"}], "channels": []}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for non-integer FARC_HTTP_PORT, got nil")
	}
}

func TestLoad_HTTPOrWSSectionInJSONRejected(t *testing.T) {
	setRequiredEnv(t)
	// http/ws/metrics moved to env -- their JSON keys must now be rejected
	// by DisallowUnknownFields, not silently ignored.
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8080},
  "storages": [{"id":"disk0","path":"/dev/sdb1"}],
  "channels": []
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for stale http section in JSON, got nil")
	}
}

func TestLoad_ScheduleRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "storages": [{"id":"disk0","path":"/dev/sdb1"}],
  "channels": [{"id":1,"rtsp_url":"rtsp://cam/1","storage":"disk0","capture_policy":{"type":"schedule"}}]
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for schedule policy, got nil")
	}
}

func TestLoad_ChannelZeroRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "storages": [{"id":"disk0","path":"/dev/sdb1"}],
  "channels": [{"id":0,"rtsp_url":"rtsp://cam/1","storage":"disk0","capture_policy":{"type":"continuous"}}]
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for channel id 0, got nil")
	}
}

func TestLoad_UnknownStorageReferenceRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "storages": [{"id":"disk0","path":"/dev/sdb1"}],
  "channels": [{"id":1,"rtsp_url":"rtsp://cam/1","storage":"nope","capture_policy":{"type":"continuous"}}]
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for unknown storage reference, got nil")
	}
}

func TestLoad_DuplicateStorageIDRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "storages": [{"id":"disk0","path":"/dev/sdb1"}, {"id":"disk0","path":"/dev/sdb2"}],
  "channels": []
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for duplicate storage id, got nil")
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "storages": [{"id":"disk0","path":"/dev/sdb1"}],
  "channels": [{"id":1,"rtsp_url":"rtsp://cam/1","storage":"disk0","capture_policy":{"type":"continuous","max_deferred_start":"30 seconds"}}]
}`
	_, err := Load(writeConfig(t, doc))
	if err == nil {
		t.Fatalf("Load: want error for invalid duration string, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	setRequiredEnv(t)
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatalf("Load: want error for missing file, got nil")
	}
}

func TestSave_RoundTripsThroughLoad(t *testing.T) {
	setRequiredEnv(t)
	path := writeConfig(t, docExample)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Storages = append(cfg.Storages, Storage{ID: "disk1", Path: "/dev/sdc1"})
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(got.Storages) != 2 || got.Storages[1].ID != "disk1" || got.Storages[1].Path != "/dev/sdc1" {
		t.Fatalf("Storages after round-trip = %+v", got.Storages)
	}
	if got.HTTP.String() != cfg.HTTP.String() || len(got.Channels) != len(cfg.Channels) {
		t.Fatalf("Save lost unrelated fields: got = %+v", got)
	}
}

func TestSave_OverwritesInPlace(t *testing.T) {
	// docker-compose.yaml keeps farc.config.json on the farc_config volume --
	// Save must overwrite the same inode, not swap it via rename, or the
	// write would silently detach from the volume-backed file under Docker.
	setRequiredEnv(t)
	path := writeConfig(t, docExample)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after Save: %v", err)
	}
	if !os.SameFile(info, after) {
		t.Fatalf("Save replaced the file's inode instead of overwriting it in place")
	}
}

func TestEnsureExists_CreatesEmptyConfigWhenMissing(t *testing.T) {
	setRequiredEnv(t)
	path := filepath.Join(t.TempDir(), "farc.config.json")

	if err := EnsureExists(path); err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after EnsureExists: %v", err)
	}
	if len(cfg.Storages) != 0 || len(cfg.Channels) != 0 {
		t.Fatalf("EnsureExists-created config = %+v, want empty storages/channels", cfg)
	}
}

func TestEnsureExists_LeavesExistingFileUntouched(t *testing.T) {
	setRequiredEnv(t)
	path := writeConfig(t, docExample)

	if err := EnsureExists(path); err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Storages) != 1 || cfg.Storages[0].ID != "disk0" {
		t.Fatalf("EnsureExists overwrote an existing config: %+v", cfg.Storages)
	}
}

func TestSave_DoesNotWriteHTTPWSMetricsToJSON(t *testing.T) {
	// HTTP/WS/Metrics are env-sourced (json:"-") -- Save must not leak them
	// back into the file, or a stale JSON section could shadow env on the
	// next Load once DisallowUnknownFields is (hypothetically) relaxed.
	setRequiredEnv(t)
	path := writeConfig(t, docExample)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{`"http"`, `"ws"`, `"metrics"`} {
		if strings.Contains(string(buf), key) {
			t.Fatalf("Save wrote %s into the JSON file: %s", key, buf)
		}
	}
}
