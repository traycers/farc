package hlsconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const docExample = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [
    {"id":"farcd0","http":"http://10.0.0.1:8080","ws":"ws://10.0.0.1:8081"}
  ],
  "channels": [
    {"id":42,"farcd":"farcd0","storage":"disk0"}
  ],
  "target_segment_duration": "6s",
  "cache_dir": "/var/cache/hls_server",
  "cache_quota_bytes": 10737418240
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

func TestLoad_ChannelZeroRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":0,"farcd":"farcd0","storage":"disk0"}],
  "target_segment_duration": "6s", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for channel id 0, got nil")
	}
}

func TestLoad_UnknownFarcdReferenceRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"nope","storage":"disk0"}],
  "target_segment_duration": "6s", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for unknown farcd reference, got nil")
	}
}

func TestLoad_DuplicateFarcdIDRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h1","ws":"ws://h1"}, {"id":"farcd0","http":"http://h2","ws":"ws://h2"}],
  "channels": [],
  "target_segment_duration": "6s", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for duplicate farcd id, got nil")
	}
}

func TestLoad_DuplicateChannelIDRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}, {"id":1,"farcd":"farcd0","storage":"disk1"}],
  "target_segment_duration": "6s", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for duplicate channel id, got nil")
	}
}

func TestLoad_MissingTargetSegmentDurationRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}],
  "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing target_segment_duration, got nil")
	}
}

func TestLoad_MissingCacheDirRejected(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}],
  "target_segment_duration": "6s"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing cache_dir, got nil")
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	const doc = `{
  "http": {"ip":"0.0.0.0","port":8090},
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [],
  "target_segment_duration": "6 seconds", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for invalid duration string, got nil")
	}
}

func TestLoad_MissingHTTPPortRejected(t *testing.T) {
	const doc = `{
  "farcds": [{"id":"farcd0","http":"http://h","ws":"ws://h"}],
  "channels": [],
  "target_segment_duration": "6s", "cache_dir": "/tmp/cache"
}`
	if _, err := Load(writeConfig(t, doc)); err == nil {
		t.Fatalf("Load: want error for missing http.port, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("Load: want error for missing file, got nil")
	}
}
