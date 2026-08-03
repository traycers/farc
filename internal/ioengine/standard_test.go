package ioengine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStandardBackendRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b, err := OpenStandard(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	defer b.Close()

	if b.Name() != "standard" {
		t.Errorf("Name() = %q, want %q", b.Name(), "standard")
	}
	if b.Alignment() != 1 {
		t.Errorf("Alignment() = %d, want 1", b.Alignment())
	}

	data := []byte("hello, direct-io-free world")
	if _, err := b.WriteAt(data, 100); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := b.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	got := make([]byte, len(data))
	if _, err := b.ReadAt(got, 100); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("ReadAt = %q, want %q", got, data)
	}
}

func TestStandardBackendOpenViaFactory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	b, err := Open(path, Options{Backend: "standard"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()
	if b.Name() != "standard" {
		t.Errorf("Name() = %q, want standard", b.Name())
	}
}

func TestOpenUnknownBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.img")
	if _, err := Open(path, Options{Backend: "bogus"}); err == nil {
		t.Fatal("expected error for unknown backend name")
	}
}
