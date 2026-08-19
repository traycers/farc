// Package msmconfig implements msm_server's process configuration --
// entirely from environment variables, unlike internal/config/internal/
// hlsconfig's env+JSON split: msm_server has no site-specific or
// runtime-mutable state to persist (outbound, it's a stateless, best-effort
// relay from farcd's WS event feed to an external msm HTTP API; inbound, it
// translates /api/v1/archives/* calls into farcd HTTP calls with no state
// of its own either -- see internal/msmd's package doc), so there is no
// JSON config file at all.
package msmconfig

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Addr is a bare ip:port server address, mirroring internal/hlsconfig.Addr
// (duplicated rather than imported -- this package's own convention of
// duplicating small shared shapes across msm_server/hls_server rather than
// cross-importing).
type Addr struct {
	IP   string
	Port int
}

func (a Addr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// Config is msm_server's whole process configuration.
type Config struct {
	// HTTP is msm_server's own inbound listen address for
	// /api/v1/archives/* (internal/archivesapi) -- the external msm/
	// controller's single integration point into this codebase.
	// MSM_SERVER_HTTP_IP (default "0.0.0.0") / MSM_SERVER_HTTP_PORT
	// (required).
	HTTP Addr

	// Metrics is msm_server's Prometheus /metrics listen address.
	// MSM_SERVER_METRICS_IP (default "0.0.0.0") / MSM_SERVER_METRICS_PORT
	// (required).
	Metrics Addr

	// FarcWS is the one farcd EventPushServer msm_server subscribes to
	// (MSM_SERVER_FARC_WS), e.g. "ws://127.0.0.1:8081" -- like hls_server
	// (ADR-020), v1 supports exactly one farcd, not a list.
	FarcWS string

	// FarcHTTP is that same farcd's HTTP API base URL
	// (MSM_SERVER_FARC_HTTP), e.g. "http://127.0.0.1:8080" -- the WS "toc"
	// push only carries the TOC section, not Content, so internal/msmd
	// fetches the actual SPS/PPS/VPS/audio-config bytes params_add needs
	// via farcd's existing content-range read API
	// (GET .../fcontainers/{uuid}?ranges=...), same as hls_server's
	// internal/hlsclient already does for its own purposes.
	FarcHTTP string

	// MSMBaseURL is the external msm service's base URL
	// (MSM_SERVER_MSM_URL), e.g. "http://msm:9000" -- where every
	// params_add/fblocks_add/fblocks_del/status_set/info_set/started_add/
	// finished_add/vaa_blocks_add call goes.
	MSMBaseURL string

	// LogDir is MSM_SERVER_LOG_DIR: a directory msm_server additionally
	// writes its own log lines to (msm_server.log), alongside stderr.
	// Optional -- empty means stderr only, matching the process's behavior
	// before this field existed.
	LogDir string
}

// Load reads Config from the environment and validates it.
func Load() (*Config, error) {
	ip := os.Getenv("MSM_SERVER_HTTP_IP")
	if ip == "" {
		ip = "0.0.0.0"
	}
	// An unset/invalid MSM_SERVER_HTTP_PORT is left as 0 rather than
	// rejected here, so validate's own "port is required" error stays the
	// single place port-missing is reported.
	port, _ := strconv.Atoi(os.Getenv("MSM_SERVER_HTTP_PORT"))

	metricsIP := os.Getenv("MSM_SERVER_METRICS_IP")
	if metricsIP == "" {
		metricsIP = "0.0.0.0"
	}
	metricsPort, _ := strconv.Atoi(os.Getenv("MSM_SERVER_METRICS_PORT"))

	cfg := &Config{
		HTTP:       Addr{IP: ip, Port: port},
		Metrics:    Addr{IP: metricsIP, Port: metricsPort},
		FarcWS:     os.Getenv("MSM_SERVER_FARC_WS"),
		FarcHTTP:   os.Getenv("MSM_SERVER_FARC_HTTP"),
		MSMBaseURL: os.Getenv("MSM_SERVER_MSM_URL"),
		LogDir:     os.Getenv("MSM_SERVER_LOG_DIR"),
	}
	err := cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("msmconfig: %w", err)
	}
	return cfg, nil
}

func (cfg *Config) validate() error {
	if cfg.HTTP.Port == 0 {
		return errors.New("MSM_SERVER_HTTP_PORT is required")
	}
	if cfg.Metrics.Port == 0 {
		return errors.New("MSM_SERVER_METRICS_PORT is required")
	}
	if cfg.FarcWS == "" {
		return errors.New("MSM_SERVER_FARC_WS is required")
	}
	if cfg.FarcHTTP == "" {
		return errors.New("MSM_SERVER_FARC_HTTP is required")
	}
	if cfg.MSMBaseURL == "" {
		return errors.New("MSM_SERVER_MSM_URL is required")
	}
	return nil
}
