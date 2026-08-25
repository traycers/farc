package v2

import "testing"

func TestEpilogRoundTrip(t *testing.T) {
	e := Epilog{
		Count: 5,
		Rows: [MaxEpilogRows]EpilogRow{
			{Type: NodeTypeRoot, ID: 0, Offset: 0, Size: 64, CRC32: 0x1111},
			{Type: NodeTypeParams, ID: 1, Offset: 64, Size: 128, CRC32: 0x2222},
			{Type: NodeTypeCatalog, ID: 2, Offset: 192, Size: 4096, CRC32: 0x3333},
			{Type: NodeTypeContent, ID: 3, Offset: 4288, Size: 1 << 20, CRC32: 0x4444},
			{Type: NodeTypeTOC, ID: 4, Offset: 1<<20 + 4288, Size: 8192, CRC32: 0x5555},
		},
	}

	buf := EncodeEpilog(e)
	if len(buf) != EpilogSizeV2 {
		t.Fatalf("encoded epilog size = %d, want %d", len(buf), EpilogSizeV2)
	}

	got, err := DecodeEpilog(buf)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if got != e {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, e)
	}
}

func TestEpilogRoundTripPartialCount(t *testing.T) {
	// Progressive write: only the first 2 of 5 nodes confirmed so far
	// (§12.5 — count grows monotonically as each node completes).
	e := Epilog{
		Count: 2,
		Rows: [MaxEpilogRows]EpilogRow{
			{Type: NodeTypeRoot, ID: 0, Offset: 0, Size: 64, CRC32: 0x1111},
			{Type: NodeTypeParams, ID: 1, Offset: 64, Size: 128, CRC32: 0x2222},
		},
	}

	buf := EncodeEpilog(e)
	got, err := DecodeEpilog(buf)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if got.Count != 2 {
		t.Fatalf("count = %d, want 2", got.Count)
	}
	if got != e {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, e)
	}
}

func TestDecodeEpilogCorruptRowCRC(t *testing.T) {
	e := Epilog{Count: 1, Rows: [MaxEpilogRows]EpilogRow{{Type: NodeTypeRoot, Size: 64}}}
	buf := EncodeEpilog(e)
	buf[9] ^= 0xFF // flip a bit inside the row area

	if _, err := DecodeEpilog(buf); err == nil {
		t.Fatalf("expected error for corrupted epilog body, got nil")
	}
}

func TestDecodeEpilogCorruptFinishMagic(t *testing.T) {
	e := Epilog{Count: 1, Rows: [MaxEpilogRows]EpilogRow{{Type: NodeTypeRoot, Size: 64}}}
	buf := EncodeEpilog(e)
	buf[len(buf)-1] ^= 0xFF

	if _, err := DecodeEpilog(buf); err == nil {
		t.Fatalf("expected error for corrupted magic_finish, got nil")
	}
}

func TestDecodeEpilogTooShort(t *testing.T) {
	if _, err := DecodeEpilog(make([]byte, 10)); err == nil {
		t.Fatalf("expected error for too-short buffer, got nil")
	}
}
