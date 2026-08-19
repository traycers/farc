package msmconfig

import "testing"

func TestLoad_RequiresFarcWS(t *testing.T) {
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	t.Setenv("MSM_SERVER_FARC_WS", "")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_FARC_WS is unset")
	}
}

func TestLoad_RequiresFarcHTTP(t *testing.T) {
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_FARC_HTTP is unset")
	}
}

func TestLoad_RequiresMSMBaseURL(t *testing.T) {
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_MSM_URL is unset")
	}
}

func TestLoad_RequiresHTTPPort(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	t.Setenv("MSM_SERVER_HTTP_PORT", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_HTTP_PORT is unset")
	}
}

func TestLoad_RequiresMetricsPort(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_METRICS_PORT is unset")
	}
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FarcWS != "ws://127.0.0.1:8081" || cfg.FarcHTTP != "http://127.0.0.1:8080" || cfg.MSMBaseURL != "http://msm:9000" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.HTTP.IP != "0.0.0.0" || cfg.HTTP.Port != 8090 {
		t.Fatalf("cfg.HTTP = %+v, want {0.0.0.0 8090}", cfg.HTTP)
	}
	if cfg.Metrics.IP != "0.0.0.0" || cfg.Metrics.Port != 9092 {
		t.Fatalf("cfg.Metrics = %+v, want {0.0.0.0 9092}", cfg.Metrics)
	}
}

func TestLoad_HTTPIPOverride(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	t.Setenv("MSM_SERVER_HTTP_PORT", "8090")
	t.Setenv("MSM_SERVER_METRICS_PORT", "9092")
	t.Setenv("MSM_SERVER_HTTP_IP", "127.0.0.1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.String() != "127.0.0.1:8090" {
		t.Fatalf("cfg.HTTP.String() = %q, want %q", cfg.HTTP.String(), "127.0.0.1:8090")
	}
}
