package v2

import (
	"encoding/binary"
	"fmt"

	"github.com/traycers/farc/fblock"
)

// FixedPrologSizeV2 is the size in bytes of the fixed part of the v2.0
// prologue (§12.2) — smaller than v1.0's FixedPrologSize (56) because
// params_size/catalog_size are dropped: node layout is now given by the
// epilog directory (§12.5), not computed from these fields.
const FixedPrologSizeV2 = 48

// FixedProlog is the fixed (version-stable) part of a v2.0 fblock's
// prologue — not a tree node, exactly like fblock.FixedProlog in v1.0
// (§12.2).
type FixedProlog struct {
	FormatVersionMajor uint16
	FormatVersionMinor uint16
	MaxChannels        uint16 // C, fixed at Storage init (ADR-014)
	WriteSequence      uint64 // monotonic per-Storage counter (ADR-008)
	CatalogTime        uint64 // Unix ns, diagnostic only, never used for ordering
	FblockSize         uint64
	CatalogEntryCount  uint32 // N — domain data (Storage's fblock count), not layout math in v2.0
}

// EncodeFixedProlog serializes p into a new FixedPrologSizeV2-byte buffer.
// It reuses fblock.MagicProlog (§5.1/§12.2: the magic and version fields
// are the part of the prologue guaranteed stable across format versions —
// a reader always finds the same magic at offset 0, then decides from
// format_version_major whether it can proceed).
func EncodeFixedProlog(p FixedProlog) []byte {
	buf := make([]byte, FixedPrologSizeV2)
	copy(buf[0:8], fblock.MagicProlog[:])
	binary.LittleEndian.PutUint16(buf[8:10], p.FormatVersionMajor)
	binary.LittleEndian.PutUint16(buf[10:12], p.FormatVersionMinor)
	binary.LittleEndian.PutUint16(buf[12:14], p.MaxChannels)
	// buf[14:16] reserved, zero
	binary.LittleEndian.PutUint64(buf[16:24], p.WriteSequence)
	binary.LittleEndian.PutUint64(buf[24:32], p.CatalogTime)
	binary.LittleEndian.PutUint64(buf[32:40], p.FblockSize)
	binary.LittleEndian.PutUint32(buf[40:44], p.CatalogEntryCount)
	// buf[44:48] reserved, zero
	return buf
}

// DecodeFixedProlog parses the fixed prologue from the start of buf.
// Returns fblock.ErrUninitialized if magic_prolog is absent — same
// sentinel as v1.0, since "uninitialized" (ADR-006) is a version-independent
// concept.
func DecodeFixedProlog(buf []byte) (FixedProlog, error) {
	if len(buf) < FixedPrologSizeV2 {
		return FixedProlog{}, fmt.Errorf("fblock/v2: buffer too short for fixed prolog: %d < %d", len(buf), FixedPrologSizeV2)
	}
	if !fblock.HasValidMagicProlog(buf) {
		return FixedProlog{}, fblock.ErrUninitialized
	}
	return FixedProlog{
		FormatVersionMajor: binary.LittleEndian.Uint16(buf[8:10]),
		FormatVersionMinor: binary.LittleEndian.Uint16(buf[10:12]),
		MaxChannels:        binary.LittleEndian.Uint16(buf[12:14]),
		WriteSequence:      binary.LittleEndian.Uint64(buf[16:24]),
		CatalogTime:        binary.LittleEndian.Uint64(buf[24:32]),
		FblockSize:         binary.LittleEndian.Uint64(buf[32:40]),
		CatalogEntryCount:  binary.LittleEndian.Uint32(buf[40:44]),
	}, nil
}
