//go:build linux

package ioengine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// openDirectOrSkip opens path with the direct backend, skipping the test if
// O_DIRECT itself isn't supported here (e.g. tmpfs — common for t.TempDir()
// in CI/sandboxes) rather than failing, per the plan's verification note.
func openDirectOrSkip(t *testing.T, path string) *DirectBackend {
	t.Helper()
	b, err := OpenDirect(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Skipf("O_DIRECT not usable on this filesystem (%s): %v", path, err)
	}
	return b
}

func TestDirectBackendRoundTripAligned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b := openDirectOrSkip(t, path)
	defer b.Close()

	align := b.Alignment()
	if align < 512 {
		t.Fatalf("unexpectedly small alignment: %d", align)
	}
	if b.Name() != "direct" {
		t.Errorf("Name() = %q, want direct", b.Name())
	}

	data := alignedBuffer(align*2, align)
	for i := range data {
		data[i] = byte(i)
	}
	if _, err := b.WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	err := b.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := make([]byte, len(data)) // deliberately NOT using alignedBuffer —
	// ReadAt must handle an arbitrary caller buffer via internal rounding.
	if _, err := b.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("read-back data does not match what was written")
	}
}

func TestDirectBackendReadAtArbitraryUnalignedRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b := openDirectOrSkip(t, path)
	defer b.Close()

	align := b.Alignment()
	full := alignedBuffer(align*4, align)
	for i := range full {
		full[i] = byte(i % 251)
	}
	if _, err := b.WriteAt(full, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	err := b.Sync()
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Request a small, unaligned sub-range straddling a block boundary.
	offset := int64(align) - 3
	length := 10
	got := make([]byte, length)
	if _, err := b.ReadAt(got, offset); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	want := full[offset : offset+int64(length)]
	if !bytes.Equal(got, want) {
		t.Errorf("unaligned ReadAt = %v, want %v", got, want)
	}
}

func TestDirectBackendWriteAtRejectsMisaligned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b := openDirectOrSkip(t, path)
	defer b.Close()

	if _, err := b.WriteAt(make([]byte, b.Alignment()), 1); !errors.Is(err, ErrMisaligned) {
		t.Errorf("WriteAt with misaligned offset = %v, want ErrMisaligned", err)
	}
	if _, err := b.WriteAt(make([]byte, b.Alignment()-1), 0); !errors.Is(err, ErrMisaligned) {
		t.Errorf("WriteAt with misaligned length = %v, want ErrMisaligned", err)
	}
}

func TestAlignedBufferIsActuallyAligned(t *testing.T) {
	for _, align := range []int{512, 4096} {
		for _, size := range []int{1, align - 1, align, align + 1, align * 3} {
			buf := alignedBuffer(size, align)
			if len(buf) != size {
				t.Fatalf("alignedBuffer(%d,%d) len = %d, want %d", size, align, len(buf), size)
			}
			addr := uintptr(unsafe.Pointer(&buf[0]))
			if addr%uintptr(align) != 0 {
				t.Fatalf("alignedBuffer(%d,%d) address %#x not aligned to %d", size, align, addr, align)
			}
		}
	}
}

func TestOpenDirectViaFactory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b, err := Open(path, Options{Backend: "direct"})
	if err != nil {
		t.Skipf("O_DIRECT not usable on this filesystem: %v", err)
	}
	defer b.Close()
	if b.Name() != "direct" {
		t.Errorf("Name() = %q, want direct", b.Name())
	}
}
