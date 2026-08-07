// Package msmconfig implements msm_server's process configuration --
// entirely from environment variables, unlike internal/config/internal/
// hlsconfig's env+JSON split: msm_server has no site-specific or
// runtime-mutable state to persist (it is a stateless, best-effort relay
// from farcd's WS event feed to an external msm HTTP API -- see internal/msmd's
// package doc), so there is no JSON config file at all.
package msmconfig

import (
	"errors"
	"fmt"
	"os"
)

// Config is msm_server's whole process configuration.
type Config struct {
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
}

// Load reads Config from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		FarcWS:     os.Getenv("MSM_SERVER_FARC_WS"),
		FarcHTTP:   os.Getenv("MSM_SERVER_FARC_HTTP"),
		MSMBaseURL: os.Getenv("MSM_SERVER_MSM_URL"),
	}
	err := cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("msmconfig: %w", err)
	}
	return cfg, nil
}

func (cfg *Config) validate() error {
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
