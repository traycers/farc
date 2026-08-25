package fblock

import "testing"

func TestHasValidMagicProlog(t *testing.T) {
	buf := make([]byte, 8)
	copy(buf, MagicProlog[:])
	if !HasValidMagicProlog(buf) {
		t.Fatalf("HasValidMagicProlog(valid magic) = false, want true")
	}
}

func TestHasValidMagicProlog_ZeroBuffer(t *testing.T) {
	// An all-zero fblock (freshly allocated space) must be detected as
	// uninitialized (ADR-006) -- v2's own DecodeFixedProlog
	// (fblock/v2/prolog.go) relies on exactly this to return
	// ErrUninitialized rather than a parse error.
	if HasValidMagicProlog(make([]byte, 8)) {
		t.Fatalf("HasValidMagicProlog(zero buffer) = true, want false")
	}
}

func TestHasValidMagicProlog_TooShort(t *testing.T) {
	if HasValidMagicProlog(make([]byte, 4)) {
		t.Fatalf("HasValidMagicProlog(too-short buffer) = true, want false")
	}
}
