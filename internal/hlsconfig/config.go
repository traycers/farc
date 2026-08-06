// Package hlsconfig implements hls_server's process configuration, split the
// same way internal/config splits farcd's (see that package's doc comment):
// server/tuning parameters come from environment variables (HLS_SERVER_
// HTTP_IP/PORT, HLS_SERVER_FARC_HTTP/WS, HLS_SERVER_TARGET_SEGMENT_DURATION,
// HLS_SERVER_CACHE_BACKEND ("disk" or "s3"), HLS_SERVER_CACHE_DIR/
// HLS_SERVER_CACHE_QUOTA_BYTES (disk backend), HLS_SERVER_S3_ENDPOINT/
// HLS_SERVER_S3_BUCKET/HLS_SERVER_S3_ACCESS_KEY/HLS_SERVER_S3_SECRET_KEY/
// HLS_SERVER_S3_USE_SSL (s3 backend)) so a working
// deployment's env can be committed to git, while the JSON file
// (docs/docs/archive/12-hls-server.md §7) keeps only the site-specific
// data: the channel -> farcd-side storage id mapping. The one farcd
// hls_server talks to (ADR-020 — v1 supports exactly one, not a list) is
// itself an env-sourced address, not JSON, like HTTP/WS/Metrics are for
// farcd's own config. The JSON file itself lives on a Docker volume rather
// than a repo file bind mount (docker-compose.yaml's hls_config volume) —
// EnsureExists seeds it with an empty-but-valid config the first time the
// volume is empty, mirroring internal/config.EnsureExists.
//
// Depends on stdlib only, per PLAN.md's package layout table.
package hlsconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Duration is a JSON-encoded Go-style duration string ("2s", "500ms" —
// time.ParseDuration), matching internal/config.Duration's own convention.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("hlsconfig: duration must be a Go-style duration string: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("hlsconfig: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Addr is a bare ip:port server address. Sourced from env, not the JSON
// file — see this package's doc comment — hence no json tags.
type Addr struct {
	IP   string
	Port int
}

func (a Addr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// Farcd is the one farcd endpoint hls_server talks to (ADR-020): its HTTP
// API base URL (internal/api/server.go's routes) and its EventPushServer
// base URL (internal/api/eventpush.go) — the same two addresses farcd's own
// config exposes as separate "http"/"ws" servers (04-storage-operations.md
// §2.1). Sourced from env (HLS_SERVER_FARC_HTTP/WS), not the JSON file —
// see this package's doc comment — hence no json tags.
type Farcd struct {
	HTTP string // e.g. "http://10.0.0.1:8080"
	WS   string // e.g. "ws://10.0.0.1:8081"
}

// Channel is one entry in the top-level "channels" list: which farcd-side
// storage id serves this channel number (on the one Farcd above).
type Channel struct {
	ID      uint16 `json:"id"`
	Storage string `json:"storage"` // farcd's own storage id (its config's storages[].id)
}

// Config is hls_server's whole process configuration: HTTP/Farcd/
// TargetSegmentDuration/CacheDir/CacheQuotaBytes come from env (loadEnv),
// Channels from the JSON file at path.
type Config struct {
	HTTP Addr `json:"-"` // hls_server's own player-facing listen address

	Farcd    Farcd     `json:"-"`
	Channels []Channel `json:"channels"`

	TargetSegmentDuration Duration `json:"-"`

	// CacheBackend selects internal/segmentcache's storage: "disk" (default)
	// for a local quota-bounded LRU cache, or "s3" for an S3-compatible
	// object store (SeaweedFS, MinIO, AWS S3, Ceph RGW, ...) shared across
	// every hls_server replica -- see internal/segmentcache's package doc.
	CacheBackend string `json:"-"`

	// CacheDir/CacheQuotaBytes are only used/required when CacheBackend is
	// "disk". CacheQuotaBytes <= 0 means unbounded (internal/segmentcache's
	// own convention).
	CacheDir        string `json:"-"`
	CacheQuotaBytes int64  `json:"-"`

	// S3* are only used/required when CacheBackend is "s3".
	S3Endpoint  string `json:"-"`
	S3Bucket    string `json:"-"`
	S3AccessKey string `json:"-"`
	S3SecretKey string `json:"-"`
	S3UseSSL    bool   `json:"-"`
}

// Load reads HTTP/Farcd/TargetSegmentDuration/CacheDir/CacheQuotaBytes from
// the environment and Channels from the JSON file at path, then validates
// the combined result.
func Load(path string) (*Config, error) {
	var cfg Config
	if err := loadEnv(&cfg); err != nil {
		return nil, err
	}

	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hlsconfig: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("hlsconfig: parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("hlsconfig: %s: %w", path, err)
	}
	return &cfg, nil
}

// Save serializes cfg's JSON-backed Channels field (json:"-" excludes
// everything else) as indented JSON and writes it to path, overwriting any
// existing content in place. Mirrors internal/config.Save's own convention,
// including its reason for writing in place rather than temp-plus-rename:
// path is a Docker volume mount (hls_config), not a repo file.
func Save(path string, cfg *Config) error {
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("hlsconfig: marshal: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("hlsconfig: write %s: %w", path, err)
	}
	return nil
}

// EnsureExists writes an empty config (no channels) to path if no file
// exists there yet; it is a no-op otherwise. Lets a fresh Docker volume —
// which starts out empty, since deploy/hls_server.config.json no longer
// ships in the repo or gets bind-mounted (docker-compose.yaml's
// hls_config volume) — bootstrap itself into a valid, loadable config on
// first container start, mirroring internal/config.EnsureExists.
func EnsureExists(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("hlsconfig: stat %s: %w", path, err)
	}
	buf, err := json.MarshalIndent(&Config{Channels: []Channel{}}, "", "  ")
	if err != nil {
		return fmt.Errorf("hlsconfig: marshal: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("hlsconfig: write %s: %w", path, err)
	}
	return nil
}

// loadEnv populates cfg's env-sourced fields. An unset HLS_SERVER_HTTP_PORT
// is left as 0 rather than rejected here, so validate's own "port is
// required" error stays the single place port-missing is reported;
// HLS_SERVER_FARC_HTTP/WS/HLS_SERVER_TARGET_SEGMENT_DURATION/CACHE_DIR are
// likewise left zero when unset and caught by validate's existing checks.
func loadEnv(cfg *Config) error {
	cfg.HTTP.IP = envOr("HLS_SERVER_HTTP_IP", "0.0.0.0")
	port, err := envInt("HLS_SERVER_HTTP_PORT")
	if err != nil {
		return err
	}
	cfg.HTTP.Port = port

	cfg.Farcd.HTTP = os.Getenv("HLS_SERVER_FARC_HTTP")
	cfg.Farcd.WS = os.Getenv("HLS_SERVER_FARC_WS")

	if v := os.Getenv("HLS_SERVER_TARGET_SEGMENT_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("hlsconfig: env HLS_SERVER_TARGET_SEGMENT_DURATION=%q: %w", v, err)
		}
		cfg.TargetSegmentDuration = Duration(d)
	}

	cfg.CacheBackend = envOr("HLS_SERVER_CACHE_BACKEND", "disk")

	cfg.CacheDir = os.Getenv("HLS_SERVER_CACHE_DIR")
	quota, err := envInt64("HLS_SERVER_CACHE_QUOTA_BYTES")
	if err != nil {
		return err
	}
	cfg.CacheQuotaBytes = quota

	cfg.S3Endpoint = os.Getenv("HLS_SERVER_S3_ENDPOINT")
	cfg.S3Bucket = os.Getenv("HLS_SERVER_S3_BUCKET")
	cfg.S3AccessKey = os.Getenv("HLS_SERVER_S3_ACCESS_KEY")
	cfg.S3SecretKey = os.Getenv("HLS_SERVER_S3_SECRET_KEY")
	cfg.S3UseSSL = os.Getenv("HLS_SERVER_S3_USE_SSL") == "true"

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
		return 0, fmt.Errorf("hlsconfig: env %s=%q: not an integer: %w", key, v, err)
	}
	return n, nil
}

func envInt64(key string) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hlsconfig: env %s=%q: not an integer: %w", key, v, err)
	}
	return n, nil
}

func (cfg *Config) validate() error {
	if cfg.HTTP.Port == 0 {
		return fmt.Errorf("http.port is required")
	}

	if cfg.Farcd.HTTP == "" {
		return fmt.Errorf("farcd.http is required")
	}
	if cfg.Farcd.WS == "" {
		return fmt.Errorf("farcd.ws is required")
	}

	channelIDs := make(map[uint16]bool, len(cfg.Channels))
	for i, c := range cfg.Channels {
		// Channel number 0 is reserved (ADR-014), same rule internal/config
		// enforces on farcd's own side.
		if c.ID == 0 {
			return fmt.Errorf("channels[%d]: id 0 is reserved (ADR-014), channel ids start at 1", i)
		}
		if channelIDs[c.ID] {
			return fmt.Errorf("channels[%d]: duplicate id %d", i, c.ID)
		}
		channelIDs[c.ID] = true
		if c.Storage == "" {
			return fmt.Errorf("channels[%d]: storage is required", i)
		}
	}

	if cfg.TargetSegmentDuration.Duration() <= 0 {
		return fmt.Errorf("target_segment_duration must be a positive duration")
	}

	switch cfg.CacheBackend {
	case "disk":
		if cfg.CacheDir == "" {
			return fmt.Errorf("cache_dir is required")
		}
	case "s3":
		if cfg.S3Endpoint == "" {
			return fmt.Errorf("s3_endpoint is required when cache_backend is \"s3\"")
		}
		if cfg.S3Bucket == "" {
			return fmt.Errorf("s3_bucket is required when cache_backend is \"s3\"")
		}
	default:
		return fmt.Errorf("cache_backend must be \"disk\" or \"s3\", got %q", cfg.CacheBackend)
	}
	return nil
}
