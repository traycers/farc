package ioengine

import "testing"

func TestRoundRange(t *testing.T) {
	cases := []struct {
		name                       string
		offset, length             int64
		align                      int
		wantOff, wantLen, wantSkip int64
	}{
		{"already aligned, exact", 0, 4096, 4096, 0, 4096, 0},
		{"unaligned start", 100, 200, 512, 0, 512, 100},
		{"unaligned end", 0, 100, 512, 0, 512, 0},
		{"spans two blocks", 500, 100, 512, 0, 1024, 500},
		{"no alignment requirement", 7, 13, 1, 7, 13, 0},
		{"no alignment requirement (0)", 7, 13, 0, 7, 13, 0},
		{"large offset", 5000, 10, 512, 4608, 512, 392},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			off, length, skip := roundRange(c.offset, c.length, c.align)
			if off != c.wantOff || length != c.wantLen || skip != c.wantSkip {
				t.Errorf("roundRange(%d,%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
					c.offset, c.length, c.align, off, length, skip, c.wantOff, c.wantLen, c.wantSkip)
			}
			// Invariants that must hold regardless of the exact numbers:
			if off%int64(max(c.align, 1)) != 0 {
				t.Errorf("aligned offset %d is not a multiple of align %d", off, c.align)
			}
			if c.align > 1 && length%int64(c.align) != 0 {
				t.Errorf("aligned length %d is not a multiple of align %d", length, c.align)
			}
			if off+length < c.offset+c.length {
				t.Errorf("aligned range [%d,%d) does not cover requested [%d,%d)", off, off+length, c.offset, c.offset+c.length)
			}
			if skip != c.offset-off {
				t.Errorf("skip %d != offset-alignedOffset %d", skip, c.offset-off)
			}
		})
	}
}

func TestIsAligned(t *testing.T) {
	if !isAligned(0, 512) || !isAligned(512, 512) || !isAligned(1024, 512) {
		t.Error("multiples of 512 should be aligned")
	}
	if isAligned(1, 512) || isAligned(511, 512) {
		t.Error("non-multiples of 512 should not be aligned")
	}
	if !isAligned(7, 1) || !isAligned(7, 0) {
		t.Error("alignment <= 1 means no requirement")
	}
}
