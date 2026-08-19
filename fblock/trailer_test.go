package fblock

import (
	"bytes"
	"testing"
)

func TestEncodeTrailer_SizeAndMagic(t *testing.T) {
	buf := EncodeTrailer(4096)
	if len(buf) != 4096 {
		t.Fatalf("len(EncodeTrailer(4096)) = %d, want 4096", len(buf))
	}
	if !bytes.HasPrefix(buf, MagicTrailer[:]) {
		t.Fatalf("EncodeTrailer(4096) does not start with MagicTrailer: %v", buf[:8])
	}
	for i, b := range buf[8:] {
		if b != 0 {
			t.Fatalf("EncodeTrailer(4096)[%d] = %d, want 0 (zero padding)", i+8, b)
		}
	}
}

func TestEncodeTrailer_MinimumAlignment(t *testing.T) {
	// An alignment smaller than the magic itself must not truncate the
	// magic -- the trailer is always at least len(MagicTrailer) bytes.
	buf := EncodeTrailer(2)
	if len(buf) != len(MagicTrailer) {
		t.Fatalf("len(EncodeTrailer(2)) = %d, want %d (magic length floor)", len(buf), len(MagicTrailer))
	}
	if !bytes.Equal(buf, MagicTrailer[:]) {
		t.Fatalf("EncodeTrailer(2) = %v, want %v", buf, MagicTrailer[:])
	}
}

func TestFindTrailer_Found(t *testing.T) {
	content := []byte("some real content bytes before the trailer")
	trailer := EncodeTrailer(64)
	buf := append(append([]byte{}, content...), trailer...)

	off, ok := FindTrailer(buf)
	if !ok {
		t.Fatal("FindTrailer: expected ok=true")
	}
	if off != int64(len(content)) {
		t.Fatalf("FindTrailer offset = %d, want %d", off, len(content))
	}
}

func TestFindTrailer_NotFound(t *testing.T) {
	buf := []byte("no trailer anywhere in this buffer at all")
	_, ok := FindTrailer(buf)
	if ok {
		t.Fatal("FindTrailer: expected ok=false")
	}
}
