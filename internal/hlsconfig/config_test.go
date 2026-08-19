package hlsconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setRequiredEnv sets the env vars loadEnv treats as required (HTTP port,
// the one farcd, target segment duration, cache dir) to docExample's own
// values, via t.Setenv so each test gets its own, automatically-restored
// environment.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_METRICS_PORT", "9091")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://10.0.0.1:8080")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://10.0.0.1:8081")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/var/cache/hls_server")
	t.Setenv("HLS_SERVER_CACHE_QUOTA_BYTES", "10737418240")
}

// docExample is the JSON-backed part of hls_server's config -- http, the
// one farcd (ADR-020), target_segment_duration/cache_dir/cache_quota_bytes
// now all come from env (setRequiredEnv), not the JSON file.
const docExample = `{
  "channels": [
    {"id":42,"storage":"disk0"}
  ]
}`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hls_server.config.json")
	err := os.WriteFile(path, []byte(contents), 0o644)
	if err != nil {
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
	if cfg.Metrics.String() != "0.0.0.0:9091" {
		t.Fatalf("Metrics = %s", cfg.Metrics)
	}
	if cfg.Farcd.HTTP != "http://10.0.0.1:8080" || cfg.Farcd.WS != "ws://10.0.0.1:8081" {
		t.Fatalf("Farcd = %+v", cfg.Farcd)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].ID != 42 || cfg.Channels[0].Storage != "disk0" {
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
	t.Setenv("HLS_SERVER_METRICS_PORT", "19091")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "2s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
	cfg, err := Load(writeConfig(t, doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.String() != "127.0.0.1:18090" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
}

func TestLoad_MissingHTTPPortEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_HTTP_PORT, got nil")
	}
}

func TestLoad_MissingMetricsPortEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_METRICS_PORT, got nil")
	}
}

func TestLoad_InvalidPortEnvRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("HLS_SERVER_HTTP_PORT", "not-a-number")

	const doc = `{"channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for non-integer HLS_SERVER_HTTP_PORT, got nil")
	}
}

func TestLoad_HTTPOrFarcdSectionInJSONRejected(t *testing.T) {
	setRequiredEnv(t)
	// http/farcd moved to env -- their JSON keys must now be rejected by
	// DisallowUnknownFields, not silently ignored.
	for _, doc := range []string{
		`{"http": {"ip":"0.0.0.0","port":8090}, "channels": []}`,
		`{"farcd": {"http":"http://h","ws":"ws://h"}, "channels": []}`,
	} {
		if _, err := Load(writeConfig(t, doc)); err == nil {
			t.Fatalf("Load(%s): want error for stale env-sourced section in JSON, got nil", doc)
		}
	}
}

func TestLoad_MissingFarcHTTPEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_FARC_HTTP, got nil")
	}
}

func TestLoad_MissingFarcWSEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_FARC_WS, got nil")
	}
}

func TestLoad_ChannelZeroRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{"channels": [{"id":0,"storage":"disk0"}]}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for channel id 0, got nil")
	}
}

func TestLoad_DuplicateChannelIDRejected(t *testing.T) {
	setRequiredEnv(t)
	const doc = `{"channels": [{"id":1,"storage":"disk0"}, {"id":1,"storage":"disk1"}]}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for duplicate channel id, got nil")
	}
}

func TestLoad_MissingTargetSegmentDurationEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": [{"id":1,"storage":"disk0"}]}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_TARGET_SEGMENT_DURATION, got nil")
	}
}

func TestLoad_MissingCacheDirEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")

	const doc = `{"channels": [{"id":1,"storage":"disk0"}]}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_CACHE_DIR, got nil")
	}
}

// TestLoad_MissingCacheDirEnvRejected_S3Backend is issue 02's config-side
// contract (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// cache_dir holds hls_server's persistent TOC cache (a dedicated subpath
// under it, decided 2026-08-13 to avoid a separate config var), which is
// needed regardless of which backend cfg.CacheBackend selects for the
// *segment* cache -- s3 exempts a deployment from a local segment cache
// dir, but never from a local TOC cache dir.
func TestLoad_MissingCacheDirEnvRejected_S3Backend(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6s")
	t.Setenv("HLS_SERVER_CACHE_BACKEND", "s3")
	t.Setenv("HLS_SERVER_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("HLS_SERVER_S3_BUCKET", "hls")

	const doc = `{"channels": [{"id":1,"storage":"disk0"}]}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing HLS_SERVER_CACHE_DIR even with cache_backend=s3, got nil")
	}
}

func TestLoad_InvalidDurationEnvRejected(t *testing.T) {
	t.Setenv("HLS_SERVER_HTTP_PORT", "8090")
	t.Setenv("HLS_SERVER_FARC_HTTP", "http://h")
	t.Setenv("HLS_SERVER_FARC_WS", "ws://h")
	t.Setenv("HLS_SERVER_TARGET_SEGMENT_DURATION", "6 seconds")
	t.Setenv("HLS_SERVER_CACHE_DIR", "/tmp/cache")

	const doc = `{"channels": []}`
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

func TestSave_RoundTripsThroughLoad(t *testing.T) {
	setRequiredEnv(t)
	path := writeConfig(t, docExample)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg.Channels = append(cfg.Channels, Channel{ID: 7, Storage: "disk1"})
	err = Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(got.Channels) != 2 || got.Channels[1].ID != 7 || got.Channels[1].Storage != "disk1" {
		t.Fatalf("Channels after round-trip = %+v", got.Channels)
	}
	if got.HTTP.String() != cfg.HTTP.String() {
		t.Fatalf("Save lost env-sourced fields: got HTTP = %v, want %v", got.HTTP, cfg.HTTP)
	}
}

func TestSave_OverwritesInPlace(t *testing.T) {
	// hls_config is a Docker named volume, like farc_config -- Save must
	// overwrite the same inode, not swap it via rename, or the write would
	// silently detach from the volume-backed file under Docker.
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
	err = Save(path, cfg)
	if err != nil {
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
	path := filepath.Join(t.TempDir(), "hls_server.config.json")

	err := EnsureExists(path)
	if err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after EnsureExists: %v", err)
	}
	if len(cfg.Channels) != 0 {
		t.Fatalf("EnsureExists-created config = %+v, want no channels", cfg.Channels)
	}
}

func TestEnsureExists_LeavesExistingFileUntouched(t *testing.T) {
	setRequiredEnv(t)
	path := writeConfig(t, docExample)

	err := EnsureExists(path)
	if err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].ID != 42 {
		t.Fatalf("EnsureExists overwrote an existing config: %+v", cfg.Channels)
	}
}
