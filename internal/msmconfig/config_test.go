package msmconfig

import "testing"

func TestLoad_RequiresFarcWS(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_FARC_WS is unset")
	}
}

func TestLoad_RequiresFarcHTTP(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_FARC_HTTP is unset")
	}
}

func TestLoad_RequiresMSMBaseURL(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error when MSM_SERVER_MSM_URL is unset")
	}
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("MSM_SERVER_FARC_WS", "ws://127.0.0.1:8081")
	t.Setenv("MSM_SERVER_FARC_HTTP", "http://127.0.0.1:8080")
	t.Setenv("MSM_SERVER_MSM_URL", "http://msm:9000")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FarcWS != "ws://127.0.0.1:8081" || cfg.FarcHTTP != "http://127.0.0.1:8080" || cfg.MSMBaseURL != "http://msm:9000" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
