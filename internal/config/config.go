// Package config implements farcd's process-wide JSON configuration file,
// exactly per docs/docs/archive/04-storage-operations.md §2.1: HTTP/WS/
// metrics server addresses, the list of Storages (id/path/catalog_path —
// deliberately no geometry or write_mode/retention.days here, since those
// are set once at Storage init time and live in the Storage's own on-disk
// header, §2.2), and the list of ingest channels (rtsp_url, target storage,
// capture_policy). Storages listed here are expected to already be
// initialized (via HttpApiServer's POST /storages, Phase 10) — this package
// only Opens them, it never Inits (see internal/farcd's package doc).
//
// Depends on stdlib only, per PLAN.md's package layout table — the
// capture_policy.type string is validated here (schedule rejected, see
// below) but translated into ingest.PolicyType one layer up, in
// internal/farcd, to avoid this package depending on internal/ingest.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Duration is a JSON-encoded Go-style duration string ("30s", "1h" —
// time.ParseDuration), not a bare number of seconds, per §2.1's own
// "Формат временных величин" note.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

// MarshalJSON is UnmarshalJSON's inverse -- needed once Save (not just
// Load) exists, or round-tripping a Config through Save would silently turn
// every duration field back into a bare nanosecond number Load then rejects.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("config: duration must be a Go-style duration string: %w", err)
	}
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Addr is a bare ip:port server address (HttpApiServer, MetricsEndpoint).
type Addr struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

func (a Addr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// WSAddr is EventPushServer's address plus its own connection limit — a
// separate server from HttpApiServer (§2.1: "отдельно от HTTP-параметров,
// это разные серверы").
type WSAddr struct {
	IP             string `json:"ip"`
	Port           int    `json:"port"`
	MaxConnections int    `json:"max_connections"`
}

func (a WSAddr) String() string { return fmt.Sprintf("%s:%d", a.IP, a.Port) }

// Storage is one entry in the top-level "storages" list. CatalogPath is
// optional (ADR-007's SSD mirror). No geometry/params fields exist here —
// see this package's doc comment on why.
type Storage struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	CatalogPath string `json:"catalog_path,omitempty"`
}

// CapturePolicyType names, as they appear in the config file's
// capture_policy.type field.
const (
	CapturePolicyContinuous = "continuous"
	CapturePolicyEvent      = "event"
	CapturePolicySchedule   = "schedule"
)

// CapturePolicy is one channel's capture_policy object (10-capture-policy.md
// §7). MaxDeferredStart is continuous-only; Prerecord/Postrecord are
// event-only — the fields not used by Type are simply left zero.
type CapturePolicy struct {
	Type             string   `json:"type"`
	MaxDeferredStart Duration `json:"max_deferred_start,omitempty"`
	Prerecord        Duration `json:"prerecord,omitempty"`
	Postrecord       Duration `json:"postrecord,omitempty"`
}

// Channel is one entry in the top-level "channels" list.
type Channel struct {
	ID            uint16        `json:"id"`
	RTSPURL       string        `json:"rtsp_url"`
	Storage       string        `json:"storage"`
	CapturePolicy CapturePolicy `json:"capture_policy"`
}

// Config is the whole process configuration file.
type Config struct {
	HTTP     Addr      `json:"http"`
	WS       WSAddr    `json:"ws"`
	Metrics  Addr      `json:"metrics"`
	Storages []Storage `json:"storages"`
	Channels []Channel `json:"channels"`
}

// Save serializes cfg as indented JSON and writes it to path, overwriting
// any existing content in place. Deliberately not a temp-file-plus-rename
// swap: internal/farcd uses this to persist a storage created at runtime
// (POST /storages) back into the same file docker-compose.yaml bind-mounts
// as a single file, and a rename would detach from that mount point rather
// than updating the file the host actually sees.
func Save(path string, cfg *Config) error {
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return &cfg, nil
}

func (cfg *Config) validate() error {
	if cfg.HTTP.Port == 0 {
		return fmt.Errorf("http.port is required")
	}
	if cfg.WS.Port == 0 {
		return fmt.Errorf("ws.port is required")
	}
	if cfg.Metrics.Port == 0 {
		return fmt.Errorf("metrics.port is required")
	}

	storageIDs := make(map[string]bool, len(cfg.Storages))
	for i, s := range cfg.Storages {
		if s.ID == "" {
			return fmt.Errorf("storages[%d]: id is required", i)
		}
		if s.Path == "" {
			return fmt.Errorf("storages[%d]: path is required", i)
		}
		if storageIDs[s.ID] {
			return fmt.Errorf("storages[%d]: duplicate id %q", i, s.ID)
		}
		storageIDs[s.ID] = true
	}

	channelIDs := make(map[uint16]bool, len(cfg.Channels))
	for i, c := range cfg.Channels {
		// Channel number 0 is reserved (ADR-014: "номер канала 0
		// зарезервирован... номера — 1..65535") -- not a real channel
		// identity a consumer can ever use.
		if c.ID == 0 {
			return fmt.Errorf("channels[%d]: id 0 is reserved (ADR-014), channel ids start at 1", i)
		}
		if channelIDs[c.ID] {
			return fmt.Errorf("channels[%d]: duplicate id %d", i, c.ID)
		}
		channelIDs[c.ID] = true
		if c.RTSPURL == "" {
			return fmt.Errorf("channels[%d]: rtsp_url is required", i)
		}
		if !storageIDs[c.Storage] {
			return fmt.Errorf("channels[%d]: storage %q is not in the storages list", i, c.Storage)
		}
		switch c.CapturePolicy.Type {
		case CapturePolicyContinuous, CapturePolicyEvent:
		case CapturePolicySchedule:
			// 10-capture-policy.md §5.3 itself calls schedule "a version
			// after event" -- internal/ingest has no PolicyType value for
			// it at all, so this is rejected here at load time rather than
			// failing later, deeper in the process, with a worse error.
			return fmt.Errorf("channels[%d]: capture_policy.type \"schedule\" is not implemented in v1", i)
		default:
			return fmt.Errorf("channels[%d]: unknown capture_policy.type %q", i, c.CapturePolicy.Type)
		}
	}
	return nil
}
