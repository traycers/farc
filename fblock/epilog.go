package fblock

import (
	"encoding/binary"
	"fmt"
)

// EpilogSize is the fixed epilogue size in bytes, read from the end of the
// fblock (docs/docs/archive/03-storage-format.md §9).
const EpilogSize = 20

// Epilog is the fblock's final fixed section.
type Epilog struct {
	CRC32Content uint32
	CRC32TOC     uint32
	TOCSize      uint32
}

// EncodeEpilog serializes e into a new EpilogSize-byte buffer.
func EncodeEpilog(e Epilog) []byte {
	buf := make([]byte, EpilogSize)
	copy(buf[0:8], MagicEpilog[:])
	binary.LittleEndian.PutUint32(buf[8:12], e.CRC32Content)
	binary.LittleEndian.PutUint32(buf[12:16], e.CRC32TOC)
	binary.LittleEndian.PutUint32(buf[16:20], e.TOCSize)
	return buf
}

// HasValidMagicEpilog reports whether the last EpilogSize bytes of a fblock
// buffer end with a valid magic_epilog. buf must be the last 8+ bytes of the
// epilogue (or the whole fblock).
func HasValidMagicEpilog(epilogBuf []byte) bool {
	return len(epilogBuf) >= 8 && [8]byte(epilogBuf[0:8]) == MagicEpilog
}

// ErrIncompleteWrite indicates the epilogue's magic is absent — the write
// never reached its final stage, i.e. the fblock is in_progress from an
// interrupted write (docs/docs/archive/03-storage-format.md §9.1).
var ErrIncompleteWrite = fmt.Errorf("fblock: incomplete write (no valid magic_epilog)")

// DecodeEpilog parses the epilogue from a EpilogSize-byte buffer (typically
// the last 20 bytes of a fblock). Returns ErrIncompleteWrite if magic_epilog
// is absent.
func DecodeEpilog(buf []byte) (Epilog, error) {
	if len(buf) < EpilogSize {
		return Epilog{}, fmt.Errorf("fblock: buffer too short for epilog: %d < %d", len(buf), EpilogSize)
	}
	if !HasValidMagicEpilog(buf[0:8]) {
		return Epilog{}, ErrIncompleteWrite
	}
	return Epilog{
		CRC32Content: binary.LittleEndian.Uint32(buf[8:12]),
		CRC32TOC:     binary.LittleEndian.Uint32(buf[12:16]),
		TOCSize:      binary.LittleEndian.Uint32(buf[16:20]),
	}, nil
}
