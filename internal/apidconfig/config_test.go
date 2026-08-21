package apidconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// setRequiredEnv sets every env var loadEnv treats as required, via
// t.Setenv so each test gets its own, automatically-restored environment.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_FARC_HTTP", "http://10.0.0.1:8080")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://mediamtx:9997")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://mediamtx:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://mediamtx:8889")
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apid.config.json")
	err := os.WriteFile(path, []byte(contents), 0o644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad_DocExample(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load(writeConfig(t, "{}"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTP.String() != "0.0.0.0:8100" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
	if cfg.Metrics.String() != "0.0.0.0:9101" {
		t.Fatalf("Metrics = %s", cfg.Metrics)
	}
	if cfg.Farcd.HTTP != "http://10.0.0.1:8080" {
		t.Fatalf("Farcd = %+v", cfg.Farcd)
	}
	if cfg.Mediamtx.APIBase != "http://mediamtx:9997" {
		t.Fatalf("Mediamtx = %+v", cfg.Mediamtx)
	}
	if cfg.Mediamtx.RTSPBase != "rtsp://mediamtx:8554" {
		t.Fatalf("Mediamtx.RTSPBase = %+v", cfg.Mediamtx)
	}
	if cfg.WebRTCPublicBase != "http://mediamtx:8889" {
		t.Fatalf("WebRTCPublicBase = %s", cfg.WebRTCPublicBase)
	}
}

func TestLoad_CustomEnvAddr(t *testing.T) {
	t.Setenv("APID_HTTP_IP", "127.0.0.1")
	t.Setenv("APID_HTTP_PORT", "18100")
	t.Setenv("APID_METRICS_PORT", "19101")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	cfg, err := Load(writeConfig(t, "{}"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.String() != "127.0.0.1:18100" {
		t.Fatalf("HTTP = %s", cfg.HTTP)
	}
}

func TestLoad_MissingHTTPPortEnvRejected(t *testing.T) {
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_HTTP_PORT, got nil")
	}
}

func TestLoad_MissingMetricsPortEnvRejected(t *testing.T) {
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_METRICS_PORT, got nil")
	}
}

func TestLoad_InvalidPortEnvRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APID_HTTP_PORT", "not-a-number")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for non-integer APID_HTTP_PORT, got nil")
	}
}

func TestLoad_MissingFarcHTTPEnvRejected(t *testing.T) {
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_FARC_HTTP, got nil")
	}
}

func TestLoad_MissingMediamtxAPIBaseEnvRejected(t *testing.T) {
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_MEDIAMTX_API_BASE, got nil")
	}
}

func TestLoad_MissingWebRTCPublicBaseEnvRejected(t *testing.T) {
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_MEDIAMTX_RTSP_BASE", "rtsp://m:8554")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_WEBRTC_PUBLIC_BASE, got nil")
	}
}

func TestLoad_MissingMediamtxRTSPBaseEnvRejected(t *testing.T) {
	t.Setenv("APID_HTTP_PORT", "8100")
	t.Setenv("APID_METRICS_PORT", "9101")
	t.Setenv("APID_FARC_HTTP", "http://h")
	t.Setenv("APID_MEDIAMTX_API_BASE", "http://m")
	t.Setenv("APID_WEBRTC_PUBLIC_BASE", "http://m:8889")

	if _, err := Load(writeConfig(t, "{}")); err == nil {
		t.Fatalf("Load: want error for missing APID_MEDIAMTX_RTSP_BASE, got nil")
	}
}

func TestLoad_HTTPOrFarcdSectionInJSONRejected(t *testing.T) {
	setRequiredEnv(t)
	// http/farcd are env-sourced -- their JSON keys must be rejected by
	// DisallowUnknownFields, not silently ignored.
	for _, doc := range []string{
		`{"http": {"ip":"0.0.0.0","port":8100}}`,
		`{"farcd": {"http":"http://h"}}`,
	} {
		if _, err := Load(writeConfig(t, doc)); err == nil {
			t.Fatalf("Load(%s): want error for stale env-sourced section in JSON, got nil", doc)
		}
	}
}

func TestLoad_MissingFile(t *testing.T) {
	setRequiredEnv(t)
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("Load: want error for missing file, got nil")
	}
}

func TestSave_OverwritesInPlace(t *testing.T) {
	// apid_config is a Docker named volume, like farc_config/hls_config --
	// Save must overwrite the same inode, not swap it via rename, or the
	// write would silently detach from the volume-backed file under Docker.
	setRequiredEnv(t)
	path := writeConfig(t, "{}")
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

func TestEnsureExists_CreatesValidConfigWhenMissing(t *testing.T) {
	setRequiredEnv(t)
	path := filepath.Join(t.TempDir(), "apid.config.json")

	err := EnsureExists(path)
	if err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load after EnsureExists: %v", err)
	}
}

func TestEnsureExists_LeavesExistingFileUntouched(t *testing.T) {
	setRequiredEnv(t)
	path := writeConfig(t, "{}")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	err = EnsureExists(path)
	if err != nil {
		t.Fatalf("EnsureExists: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after EnsureExists: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("EnsureExists overwrote an existing config: before=%q after=%q", before, after)
	}
}
