// Package apidconfig implements apid's process configuration, split the
// same way internal/config/internal/hlsconfig already are (see
// internal/config's own doc comment): server/tuning parameters and the
// addresses of the other services apid talks to come from environment
// variables (APID_HTTP_IP/PORT, APID_METRICS_IP/PORT, APID_FARC_HTTP,
// APID_MEDIAMTX_API_BASE, APID_WEBRTC_PUBLIC_BASE, APID_LOG_DIR), while the
// JSON file at the configured path holds site-specific mutable data. apid
// currently has none (it derives everything it needs from farcd/mediamtx
// on demand, see .scratch/live-page/spec.md), so the file is just "{}"
// today -- Save/EnsureExists/Load exist anyway, for the same reason
// internal/hlsconfig keeps them: so a future JSON-backed field is a small
// diff to an established pattern, not a new one.
//
// Depends on stdlib only, per PLAN.md's package layout table.
package apidconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Addr is a bare ip:port server address. Sourced from env, not the JSON
// file -- see this package's doc comment -- hence no json tags.
type Addr struct {
	IP   string
	Port int
}

func (a Addr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// Farcd is the farcd endpoint apid talks to for channel create/update/
// remove (internal/api/channels.go's routes). Sourced from env
// (APID_FARC_HTTP), not the JSON file -- hence no json tags.
type Farcd struct {
	HTTP string // e.g. "http://10.0.0.1:8080"
}

// Mediamtx is the mediamtx instance apid talks to via its control API
// (https://mediamtx.org/docs/features/control-api) to add/patch/delete
// paths. Sourced from env (APID_MEDIAMTX_API_BASE/APID_MEDIAMTX_RTSP_BASE),
// not the JSON file -- hence no json tags.
type Mediamtx struct {
	APIBase string // e.g. "http://mediamtx:9997"

	// RTSPBase is mediamtx's RTSP re-serve base (its default rtspAddress,
	// ":8554") -- farcd's ingest pulls a channel's stream from
	// "{RTSPBase}/{channel_id}" instead of the camera directly
	// (.scratch/live-page/spec.md's "single RTSP connection to the
	// camera" decision), so apid builds each channel's farcd-side
	// rtsp_url from this.
	RTSPBase string // e.g. "rtsp://mediamtx:8554"
}

// Config is apid's whole process configuration.
type Config struct {
	HTTP    Addr `json:"-"` // apid's own listen address (web app talks to this)
	Metrics Addr `json:"-"` // Prometheus /metrics listen address

	Farcd    Farcd    `json:"-"`
	Mediamtx Mediamtx `json:"-"`

	// WebRTCPublicBase is the base URL a *browser* uses to reach mediamtx's
	// WHEP endpoint (mediamtx's webrtcAddress, default ":8889") -- may
	// differ from Mediamtx.APIBase's host/port, since APIBase only needs to
	// be reachable from apid's own container, not from the public web.
	// apid appends "/{channel_id}/whep" to this to build each channel's
	// whep_url (.scratch/live-page/issues/01-apid-server.md).
	WebRTCPublicBase string `json:"-"`

	// LogDir is APID_LOG_DIR: a directory apid additionally writes its own
	// log lines to (apid.log), alongside stderr. Optional -- empty means
	// stderr only, matching farcd/hlsd's own LogDir convention.
	LogDir string `json:"-"`
}

// Load reads the env-sourced fields from the environment and decodes the
// JSON file at path (currently just "{}" -- see this package's doc
// comment) into the same Config, then validates the combined result.
func Load(path string) (*Config, error) {
	var cfg Config
	err := loadEnv(&cfg)
	if err != nil {
		return nil, err
	}

	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("apidconfig: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	err = dec.Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("apidconfig: parse %s: %w", path, err)
	}
	err = cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("apidconfig: %s: %w", path, err)
	}
	return &cfg, nil
}

// Save serializes cfg's JSON-backed fields (json:"-" excludes everything
// else, i.e. today, everything) as indented JSON and writes it to path,
// overwriting any existing content in place -- path is a Docker volume
// mount, not a repo file, mirroring internal/config.Save/
// internal/hlsconfig.Save's own convention and reasoning.
func Save(path string, cfg *Config) error {
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("apidconfig: marshal: %w", err)
	}
	err = os.WriteFile(path, buf, 0o600)
	if err != nil {
		return fmt.Errorf("apidconfig: write %s: %w", path, err)
	}
	return nil
}

// EnsureExists writes an empty-but-valid config ("{}") to path if no file
// exists there yet; it is a no-op otherwise. Lets a fresh Docker volume
// bootstrap itself into a valid, loadable config on first container
// start, mirroring internal/config.EnsureExists/internal/hlsconfig.EnsureExists.
func EnsureExists(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("apidconfig: stat %s: %w", path, err)
	}
	buf, err := json.MarshalIndent(&Config{}, "", "  ")
	if err != nil {
		return fmt.Errorf("apidconfig: marshal: %w", err)
	}
	err = os.WriteFile(path, buf, 0o600)
	if err != nil {
		return fmt.Errorf("apidconfig: write %s: %w", path, err)
	}
	return nil
}

// loadEnv populates cfg's env-sourced fields. An unset APID_HTTP_PORT is
// left as 0 rather than rejected here, so validate's own "port is
// required" error stays the single place port-missing is reported --
// APID_FARC_HTTP/APID_MEDIAMTX_API_BASE/APID_WEBRTC_PUBLIC_BASE are
// likewise left empty when unset and caught by validate's existing checks.
func loadEnv(cfg *Config) error {
	cfg.HTTP.IP = envOr("APID_HTTP_IP", "0.0.0.0")
	port, err := envInt("APID_HTTP_PORT")
	if err != nil {
		return err
	}
	cfg.HTTP.Port = port

	cfg.Metrics.IP = envOr("APID_METRICS_IP", "0.0.0.0")
	cfg.Metrics.Port, err = envInt("APID_METRICS_PORT")
	if err != nil {
		return err
	}

	cfg.Farcd.HTTP = os.Getenv("APID_FARC_HTTP")
	cfg.Mediamtx.APIBase = os.Getenv("APID_MEDIAMTX_API_BASE")
	cfg.Mediamtx.RTSPBase = os.Getenv("APID_MEDIAMTX_RTSP_BASE")
	cfg.WebRTCPublicBase = os.Getenv("APID_WEBRTC_PUBLIC_BASE")

	cfg.LogDir = os.Getenv("APID_LOG_DIR")

	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("apidconfig: env %s=%q: not an integer: %w", key, v, err)
	}
	return n, nil
}

func (cfg *Config) validate() error {
	if cfg.HTTP.Port == 0 {
		return errors.New("http.port is required")
	}
	if cfg.Metrics.Port == 0 {
		return errors.New("metrics.port is required")
	}
	if cfg.Farcd.HTTP == "" {
		return errors.New("farcd.http is required")
	}
	if cfg.Mediamtx.APIBase == "" {
		return errors.New("mediamtx.api_base is required")
	}
	if cfg.Mediamtx.RTSPBase == "" {
		return errors.New("mediamtx.rtsp_base is required")
	}
	if cfg.WebRTCPublicBase == "" {
		return errors.New("webrtc_public_base is required")
	}
	return nil
}
