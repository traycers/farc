package toc

import "testing"

func TestComputeOffsetsAlignment(t *testing.T) {
	for _, n := range []uint32{0, 1, 7, 64, 65, 1000, 1_000_000} {
		offs := ComputeOffsets(n)
		for _, off := range []uint32{offs.Type, offs.Role, offs.Parent, offs.Sibling, offs.ValueOrOffset} {
			if off%Align != 0 {
				t.Errorf("n=%d: offset %d not %d-aligned", n, off, Align)
			}
		}
		// Size (the last column) is unpadded — no alignment guarantee, but
		// its start must still follow directly after ValueOrOffset's column.
		wantSizeOff := offs.ValueOrOffset + n*8 + pad(n, 8)
		if offs.Size != wantSizeOff {
			t.Errorf("n=%d: Size offset = %d, want %d", n, offs.Size, wantSizeOff)
		}
	}
}

func TestPaddingCostBound(t *testing.T) {
	// docs/docs/archive/06-toc-format.md §3: at most 5*63=315 bytes total
	// padding for the whole TOC section, independent of n (5 boundaries
	// between the 6 columns; the header itself is already 64-aligned).
	for _, n := range []uint32{1, 3, 100, 1234, 999_999} {
		offs := ComputeOffsets(n)
		unpadded := uint32(HeaderSize) + n*uint32(1+2+4+4+8+8)
		total := offs.Total
		if total < unpadded {
			t.Fatalf("n=%d: total %d < unpadded %d", n, total, unpadded)
		}
		if got := total - unpadded; got > 5*63 {
			t.Errorf("n=%d: padding overhead %d exceeds bound 315", n, got)
		}
	}
}

func TestEncodeDecodeEmptyColumns(t *testing.T) {
	c := &Columns{VersionMajor: 1, VersionMinor: 0, N: 0}
	buf, err := Encode(c)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(buf) != HeaderSize {
		t.Fatalf("encoded empty TOC len = %d, want %d (header only)", len(buf), HeaderSize)
	}
	got, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.N != 0 || got.VersionMajor != 1 {
		t.Errorf("decoded empty columns mismatch: %+v", got)
	}
}

func TestDecodeTooShort(t *testing.T) {
	_, err := Decode(make([]byte, 10))
	if err == nil {
		t.Fatal("expected error for buffer shorter than header")
	}
}
