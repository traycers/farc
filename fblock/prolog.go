package fblock

import "errors"

// ErrUninitialized indicates the fblock has no valid magic_prolog — it was
// never written (ADR-006), not corrupted.
var ErrUninitialized = errors.New("fblock: uninitialized (no valid magic_prolog)")

// HasValidMagicProlog reports whether buf starts with a valid magic_prolog.
// buf must be at least 8 bytes.
func HasValidMagicProlog(buf []byte) bool {
	return len(buf) >= 8 && [8]byte(buf[0:8]) == MagicProlog
}
