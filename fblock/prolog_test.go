package fblock

import (
	"bytes"
	"errors"
	"testing"
)

func TestFixedPrologRoundTrip(t *testing.T) {
	p := FixedProlog{
		FormatVersionMajor: 1,
		FormatVersionMinor: 0,
		MaxChannels:        256,
		WriteSequence:      42,
		CatalogTime:        1234567890,
		FblockSize:         1 << 20,
		ParamsSize:         123,
		CatalogEntryCount:  1000,
		CatalogSize:        65000,
	}
	buf := EncodeFixedProlog(p)
	if len(buf) != FixedPrologSize {
		t.Fatalf("encoded size = %d, want %d", len(buf), FixedPrologSize)
	}
	if !bytes.Equal(buf[0:8], MagicProlog[:]) {
		t.Fatalf("magic_prolog not written correctly")
	}

	got, err := DecodeFixedProlog(buf)
	if err != nil {
		t.Fatalf("DecodeFixedProlog: %v", err)
	}
	if got != p {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
}

func TestFixedPrologReservedBytesZero(t *testing.T) {
	buf := EncodeFixedProlog(FixedProlog{})
	if buf[14] != 0 || buf[15] != 0 {
		t.Errorf("reserved bytes at 14:16 not zero")
	}
	for i := 52; i < 56; i++ {
		if buf[i] != 0 {
			t.Errorf("reserved byte at %d not zero", i)
		}
	}
}

func TestUninitializedDetection(t *testing.T) {
	// An all-zero fblock (freshly allocated space) must be detected as
	// uninitialized, not as a parse error (ADR-006).
	buf := make([]byte, FixedPrologSize)
	if HasValidMagicProlog(buf) {
		t.Fatalf("all-zero buffer must not have a valid magic_prolog")
	}
	_, err := DecodeFixedProlog(buf)
	if !errors.Is(err, ErrUninitialized) {
		t.Fatalf("DecodeFixedProlog on zero buffer: got err %v, want ErrUninitialized", err)
	}
}

func TestDecodeFixedPrologTooShort(t *testing.T) {
	_, err := DecodeFixedProlog(make([]byte, 10))
	if err == nil {
		t.Fatalf("expected error for too-short buffer")
	}
}
