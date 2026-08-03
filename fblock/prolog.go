package fblock

import (
	"encoding/binary"
	"fmt"
)

// FixedPrologSize is the size in bytes of the fixed part of the prologue.
// This layout never changes across format versions (03-storage-format.md §5.1).
const FixedPrologSize = 56

// FixedProlog is the fixed (version-stable) part of a fblock's prologue.
type FixedProlog struct {
	FormatVersionMajor uint16
	FormatVersionMinor uint16
	MaxChannels        uint16 // C, fixed at Storage init (ADR-014)
	WriteSequence      uint64 // monotonic per-Storage counter (ADR-008)
	CatalogTime        uint64 // Unix ns, diagnostic only, never used for ordering
	FblockSize         uint64
	ParamsSize         uint32
	CatalogEntryCount  uint32 // N
	CatalogSize        uint32
}

// EncodeFixedProlog serializes p into a new FixedPrologSize-byte buffer.
func EncodeFixedProlog(p FixedProlog) []byte {
	buf := make([]byte, FixedPrologSize)
	copy(buf[0:8], MagicProlog[:])
	binary.LittleEndian.PutUint16(buf[8:10], p.FormatVersionMajor)
	binary.LittleEndian.PutUint16(buf[10:12], p.FormatVersionMinor)
	binary.LittleEndian.PutUint16(buf[12:14], p.MaxChannels)
	// buf[14:16] reserved, zero
	binary.LittleEndian.PutUint64(buf[16:24], p.WriteSequence)
	binary.LittleEndian.PutUint64(buf[24:32], p.CatalogTime)
	binary.LittleEndian.PutUint64(buf[32:40], p.FblockSize)
	binary.LittleEndian.PutUint32(buf[40:44], p.ParamsSize)
	binary.LittleEndian.PutUint32(buf[44:48], p.CatalogEntryCount)
	binary.LittleEndian.PutUint32(buf[48:52], p.CatalogSize)
	// buf[52:56] reserved, zero
	return buf
}

// ErrUninitialized indicates the fblock has no valid magic_prolog — it was
// never written (ADR-006), not corrupted.
var ErrUninitialized = fmt.Errorf("fblock: uninitialized (no valid magic_prolog)")

// HasValidMagicProlog reports whether buf starts with a valid magic_prolog.
// buf must be at least 8 bytes.
func HasValidMagicProlog(buf []byte) bool {
	return len(buf) >= 8 && [8]byte(buf[0:8]) == MagicProlog
}

// DecodeFixedProlog parses the fixed prologue from the start of buf.
// Returns ErrUninitialized if magic_prolog is absent — this is the documented
// way to distinguish `uninitialized` from `bad` (whose header was once valid).
func DecodeFixedProlog(buf []byte) (FixedProlog, error) {
	if len(buf) < FixedPrologSize {
		return FixedProlog{}, fmt.Errorf("fblock: buffer too short for fixed prolog: %d < %d", len(buf), FixedPrologSize)
	}
	if !HasValidMagicProlog(buf) {
		return FixedProlog{}, ErrUninitialized
	}
	return FixedProlog{
		FormatVersionMajor: binary.LittleEndian.Uint16(buf[8:10]),
		FormatVersionMinor: binary.LittleEndian.Uint16(buf[10:12]),
		MaxChannels:        binary.LittleEndian.Uint16(buf[12:14]),
		WriteSequence:      binary.LittleEndian.Uint64(buf[16:24]),
		CatalogTime:        binary.LittleEndian.Uint64(buf[24:32]),
		FblockSize:         binary.LittleEndian.Uint64(buf[32:40]),
		ParamsSize:         binary.LittleEndian.Uint32(buf[40:44]),
		CatalogEntryCount:  binary.LittleEndian.Uint32(buf[44:48]),
		CatalogSize:        binary.LittleEndian.Uint32(buf[48:52]),
	}, nil
}
