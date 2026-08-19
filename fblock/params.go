package fblock

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Write modes (docs/docs/archive/11-service-composition.md §3, §5.2.4).
const (
	WriteModeCyclic        = "cyclic"
	WriteModeFillUntilFull = "fill_until_full"
)

// DefaultMinContainerShare is the default minimum fraction of fblock_size
// guaranteed to fcontainer capacity (ADR-013).
const DefaultMinContainerShare = 0.7

// DefaultFlushTimeoutNS is ADR-017's periodic-flush timeout T, applied by
// DecodeParams when flush_timeout_ns is absent/zero — 5s.
const DefaultFlushTimeoutNS = int64(5_000_000_000)

// Retention holds the retention.days nested JSON param.
type Retention struct {
	Days int64 `json:"days"`
}

// Params is the JSON parameter section stored in a fblock's prologue
// (docs/docs/archive/03-storage-format.md §5.2, ADR-009). Serialized as
// strict UTF-8 JSON, no BOM. Unknown keys are ignored on read (forward
// compatibility); required keys missing on read are a format error.
type Params struct {
	FchunkSize        int64     `json:"fchunk_size"`
	ReadChunkSize     int64     `json:"read_chunk_size,omitempty"`
	FlushTimeoutNS    int64     `json:"flush_timeout_ns,omitempty"` // ADR-017's T; DecodeParams defaults absent/zero to DefaultFlushTimeoutNS -- a raw literal (e.g. in tests) left at 0 disables the timeout trigger (fchunk_size-only pacing)
	WriteMode         string    `json:"write_mode"`
	Retention         Retention `json:"retention"`
	MinContainerShare float64   `json:"min_container_share"`
}

// EncodeParams serializes p to its on-disk JSON representation.
func EncodeParams(p Params) ([]byte, error) {
	err := ValidateParams(p)
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

// DecodeParams parses the JSON parameter section. Missing required fields
// (fchunk_size, write_mode) are a format error per the evolution rules in
// 03-storage-format.md §5.2.
func DecodeParams(buf []byte) (Params, error) {
	var p Params
	err := json.Unmarshal(buf, &p)
	if err != nil {
		return Params{}, fmt.Errorf("fblock: params JSON decode: %w", err)
	}
	if p.FchunkSize == 0 {
		return Params{}, errors.New("fblock: params: missing required field fchunk_size")
	}
	if p.WriteMode == "" {
		return Params{}, errors.New("fblock: params: missing required field write_mode")
	}
	if p.ReadChunkSize == 0 {
		p.ReadChunkSize = p.FchunkSize // default per §5.2 table
	}
	if p.FlushTimeoutNS == 0 {
		p.FlushTimeoutNS = DefaultFlushTimeoutNS
	}
	if p.MinContainerShare == 0 {
		p.MinContainerShare = DefaultMinContainerShare
	}
	err = ValidateParams(p)
	if err != nil {
		return Params{}, err
	}
	return p, nil
}

// ValidateParams checks the closed set of constraints the docs place on
// these fields (write_mode enum, fchunk_size positivity).
func ValidateParams(p Params) error {
	if p.FchunkSize <= 0 {
		return fmt.Errorf("fblock: params: fchunk_size must be positive, got %d", p.FchunkSize)
	}
	if p.WriteMode != WriteModeCyclic && p.WriteMode != WriteModeFillUntilFull {
		return fmt.Errorf("fblock: params: write_mode must be %q or %q, got %q", WriteModeCyclic, WriteModeFillUntilFull, p.WriteMode)
	}
	if p.MinContainerShare < 0 || p.MinContainerShare > 1 {
		return fmt.Errorf("fblock: params: min_container_share must be in [0,1], got %v", p.MinContainerShare)
	}
	return nil
}
