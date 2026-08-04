// Package hlsconfig implements hls_server's process-wide JSON configuration
// file, mirroring internal/config's style (strict JSON, stdlib only):
// hls_server's own player-facing listen address, the farcd endpoints it
// talks to, the channel -> (farcd endpoint, farcd-side storage id) mapping,
// and the segment/cache tuning (docs/docs/archive/12-hls-server.md §7).
//
// Depends on stdlib only, per PLAN.md's package layout table.
package hlsconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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

// Addr is a bare ip:port server address.
type Addr struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

func (a Addr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// Farcd is one farcd endpoint hls_server talks to: its HTTP API base URL
// (internal/api/server.go's routes) and its EventPushServer base URL
// (internal/api/eventpush.go) — the same two addresses farcd's own config
// exposes as separate "http"/"ws" servers (04-storage-operations.md §2.1).
type Farcd struct {
	ID   string `json:"id"`
	HTTP string `json:"http"` // e.g. "http://10.0.0.1:8080"
	WS   string `json:"ws"`   // e.g. "ws://10.0.0.1:8081"
}

// Channel is one entry in the top-level "channels" list: which farcd
// endpoint and which farcd-side storage id serves this channel number.
type Channel struct {
	ID      uint16 `json:"id"`
	Farcd   string `json:"farcd"`   // references a Farcd.ID above
	Storage string `json:"storage"` // farcd's own storage id (its config's storages[].id)
}

// Config is hls_server's whole process configuration file.
type Config struct {
	HTTP Addr `json:"http"` // hls_server's own player-facing listen address

	Farcds   []Farcd   `json:"farcds"`
	Channels []Channel `json:"channels"`

	TargetSegmentDuration Duration `json:"target_segment_duration"`
	CacheDir              string   `json:"cache_dir"`
	// CacheQuotaBytes <= 0 means unbounded (internal/segmentcache's own
	// convention).
	CacheQuotaBytes int64 `json:"cache_quota_bytes"`
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hlsconfig: read %s: %w", path, err)
	}
	var cfg Config
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

func (cfg *Config) validate() error {
	if cfg.HTTP.Port == 0 {
		return fmt.Errorf("http.port is required")
	}

	farcdIDs := make(map[string]bool, len(cfg.Farcds))
	for i, f := range cfg.Farcds {
		if f.ID == "" {
			return fmt.Errorf("farcds[%d]: id is required", i)
		}
		if f.HTTP == "" {
			return fmt.Errorf("farcds[%d]: http is required", i)
		}
		if f.WS == "" {
			return fmt.Errorf("farcds[%d]: ws is required", i)
		}
		if farcdIDs[f.ID] {
			return fmt.Errorf("farcds[%d]: duplicate id %q", i, f.ID)
		}
		farcdIDs[f.ID] = true
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
		// Fails fast here, at load time, rather than at first playback
		// request — the mismatch PLAN.md's Gap resolutions flags as
		// otherwise-undetected "канал -> хранилище duplication" between
		// farcd's own config and this one.
		if !farcdIDs[c.Farcd] {
			return fmt.Errorf("channels[%d]: farcd %q is not in the farcds list", i, c.Farcd)
		}
	}

	if cfg.TargetSegmentDuration.Duration() <= 0 {
		return fmt.Errorf("target_segment_duration must be a positive duration")
	}
	if cfg.CacheDir == "" {
		return fmt.Errorf("cache_dir is required")
	}
	return nil
}
