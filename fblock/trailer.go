package fblock

import "bytes"

// EncodeTrailer returns an alignment-byte buffer: MagicTrailer followed by
// zero padding. This is the whole in_progress-only "current live end of the
// content stream" written by every periodic-flush trigger (ADR-017's
// combined data+magic write) and overwritten forward by the next one.
func EncodeTrailer(alignment int) []byte {
	if alignment < len(MagicTrailer) {
		alignment = len(MagicTrailer)
	}
	buf := make([]byte, alignment)
	copy(buf, MagicTrailer[:])
	return buf
}

// FindTrailer returns the offset within buf of the first MagicTrailer
// occurrence, for crash recovery locating the current live end of a
// partially-written in_progress fblock's content stream.
func FindTrailer(buf []byte) (offset int64, ok bool) {
	idx := bytes.Index(buf, MagicTrailer[:])
	if idx < 0 {
		return 0, false
	}
	return int64(idx), true
}
