package hlsconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setRequiredEnv sets the env vars loadEnv treats as required (HTTP port,
// target segment duration, cache dir) to docExample's own values, via
// t.Setenv so each test gets its own, automatically-restored environment.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/var/cache/hls_server")
	t.Setenv("HLS_SERVER_CACHE_QUOTA_BYTES", "10737418240")
}

// docExample is the JSON-backed part of hls_server's config -- http/
// target_segment_duration/cache_dir/cache_quota_bytes now come from env
// (setRequiredEnv), not the JSON file.
const docExample = `{
  "farcds": [
    {"id":"farcd0","http":"http://10.0.0.1:8080","ws":"ws://10.0.0.1:8081"}
  ],
  "channels": [
    {"id":42,"farcd":"farcd0","storage":"disk0"}
  ]
}`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hls_server.config.json")
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

	if cfg.HTTP.String() != "0.0.0.0:8090" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
	if len(cfg.Farcds) != 1 || cfg.Farcds[0].ID != "farcd0" || cfg.Farcds[0].HTTP != "http://10.0.0.1:8080" || cfg.Farcds[0].WS != "ws://10.0.0.1:8081" {
		t.Fatalf("Farcds = %+v", cfg.Farcds)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].ID != 42 || cfg.Channels[0].Farcd != "farcd0" || cfg.Channels[0].Storage != "disk0" {
		t.Fatalf("Channels = %+v", cfg.Channels)
	}
	if cfg.TargetSegmentDuration.Duration() != 6*time.Second {
		t.Fatalf("TargetSegmentDuration = %v", cfg.TargetSegmentDuration.Duration())
	}
	if cfg.CacheDir != "/var/cache/hls_server" {
		t.Fatalf("CacheDir = %s", cfg.CacheDir)
	}
	if cfg.CacheQuotaBytes != 10737418240 {
		t.Fatalf("CacheQuotaBytes = %d", cfg.CacheQuotaBytes)
	}
}

func TestLoad_CustomEnvAddr(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_IP", "127.0.0.1")
	t.Setenv("HLS_SERVER_HTTP_PORT", "18090")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "2s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"farcds": [], "channels": []}`
	cfg, err := Load(writeConfig(t, doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.String() != "127.0.0.1:18090" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
}

func TestLoad_MissingHTTPPortEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"farcds": [], "channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_HTTP_PORT, got nil")
	}
}

func TestLoad_InvalidPortEnvRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("HLS_SERVER_HTTP_PORT", "not-a-number")

	const doc = `{"farcds": [], "channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for non-integer HLS_SERVER_HTTP_PORT, got nil")
	}
}

func TestLoad_HTTPSectionInJSONRejected(t *testing.T) {
	setRequiredEnv(t)
	// http moved to env -- its JSON key must now be rejected by
	// DisallowUnknownFields, not silently ignored.
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [], "channels": []
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for stale http section in JSON, got nil")
	}
}

func TestLoad_ChannelZeroRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":0,"farcd":"farcd0","storage":"disk0"}]
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for channel id 0, got nil")
	}
}

func TestLoad_UnknownFarcdReferenceRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"nope","storage":"disk0"}]
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for unknown farcd reference, got nil")
	}
}

func TestLoad_DuplicateFarcdIDRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h1","ws":"ws://h1"}, {"id":"farcd0","http":"http://h2","ws":"ws://h2"}],
  "channels": []
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for duplicate farcd id, got nil")
	}
}

func TestLoad_DuplicateChannelIDRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}, {"id":1,"farcd":"farcd0","storage":"disk1"}]
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for duplicate channel id, got nil")
	}
}

func TestLoad_MissingTargetSegmentDurationEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}]
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_TARGET_SEGMENT_DURATION, got nil")
	}
}

func TestLoad_MissingCacheDirEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")

	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}]
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_CACHE_DIR, got nil")
	}
}

func TestLoad_InvalidDurationEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6 seconds")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"farcds": [], "channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for invalid duration string, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	setRequiredEnv(t)
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("Load: want error for missing file, got nil")
	}
}

