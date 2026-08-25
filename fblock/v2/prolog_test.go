package v2

import (
	"errors"
	"testing"

	"github.com/traycers/farc/fblock"
)

func TestFixedPrologRoundTrip(t *testing.T) {
	p := FixedProlog{
		FormatVersionMajor: 2,
		FormatVersionMinor: 0,
		MaxChannels:        256,
		WriteSequence:      42,
		CatalogTime:        1234567890,
		FblockSize:         1 << 20,
		CatalogEntryCount:  1000,
	}

	buf := EncodeFixedProlog(p)
	if len(buf) != FixedPrologSizeV2 {
		t.Fatalf("encoded size = %d, want %d", len(buf), FixedPrologSizeV2)
	}

	got, err := DecodeFixedProlog(buf)
	if err != nil {
		t.Fatalf("DecodeFixedProlog: %v", err)
	}
	if got != p {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
}

func TestFixedPrologUninitializedDetection(t *testing.T) {
	// Same magic_prolog as v1.0 (§5.1: the magic/version fields never
	// change structure across versions) — an all-zero buffer still reads
	// as uninitialized, not a parse error (ADR-006).
	buf := make([]byte, FixedPrologSizeV2)
	_, err := DecodeFixedProlog(buf)
	if !errors.Is(err, fblock.ErrUninitialized) {
		t.Fatalf("DecodeFixedProlog on zero buffer: got err %v, want fblock.ErrUninitialized", err)
	}
}

func TestFixedPrologTooShort(t *testing.T) {
	if _, err := DecodeFixedProlog(make([]byte, 10)); err == nil {
		t.Fatalf("expected error for too-short buffer, got nil")
	}
}
