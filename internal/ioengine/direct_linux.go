//go:build linux

package ioengine

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DirectBackend opens a file or block device with O_DIRECT, bypassing the
// OS page cache — write-verify readback through it proves data reached the
// media, not just the OS (ADR-010). Not safe for concurrent use: like every
// IoBackend, it is meant to be driven by a single StorageEngine loop that
// already serializes all disk commands (docs/docs/archive/02-storage.md
// §9.3).
type DirectBackend struct {
	fd        int
	alignment int
	scratch   []byte // reusable aligned scratch buffer for ReadAt rounding
}

// OpenDirect opens path with O_DIRECT and probes its required alignment
// (the device's logical block size for a block device, 4KiB default for a
// regular file, docs/docs/archive/adr/010-direct-io.md).
func OpenDirect(path string, flag int, perm os.FileMode) (*DirectBackend, error) {
	fd, err := unix.Open(path, flag|unix.O_DIRECT, uint32(perm))
	if err != nil {
		return nil, fmt.Errorf("ioengine: O_DIRECT open %s: %w", path, err)
	}
	align, err := probeAlignment(fd)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("ioengine: probe alignment for %s: %w", path, err)
	}
	return &DirectBackend{fd: fd, alignment: align}, nil
}

func probeAlignment(fd int) (int, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFBLK {
		sz, err := unix.IoctlGetInt(fd, unix.BLKSSZGET)
		if err != nil {
			return 0, err
		}
		return sz, nil
	}
	return 4096, nil
}

// ReadAt satisfies an arbitrary [offset, offset+len(p)) request by rounding
// outward to Alignment(), reading into an internal aligned buffer, and
// copying out only the requested bytes — invisible to the caller (ADR-010).
func (b *DirectBackend) ReadAt(p []byte, offset int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	alignedOffset, alignedLength, skip := roundRange(offset, int64(len(p)), b.alignment)
	buf := b.scratchBuf(int(alignedLength))
	n, err := unix.Pread(b.fd, buf, alignedOffset)
	if err != nil {
		return 0, err
	}
	if int64(n) < skip+int64(len(p)) {
		return 0, io.ErrUnexpectedEOF
	}
	copy(p, buf[skip:skip+int64(len(p))])
	return len(p), nil
}

// WriteAt requires offset and len(p) to already be Alignment()-sized
// (docs/docs/archive/adr/017-periodic-fchunk-flush.md — the caller produces
// aligned pieces; this backend does not silently pad or buffer partial
// writes).
func (b *DirectBackend) WriteAt(p []byte, offset int64) (int, error) {
	if !isAligned(offset, b.alignment) || !isAligned(int64(len(p)), b.alignment) {
		return 0, ErrMisaligned
	}
	return unix.Pwrite(b.fd, p, offset)
}

// Sync calls fdatasync — durability before committing `ready` in the
// indexes (docs/docs/archive/adr/010-direct-io.md: O_DIRECT bypasses the
// page cache but not the device's own volatile cache).
func (b *DirectBackend) Sync() error {
	return unix.Fdatasync(b.fd)
}

func (b *DirectBackend) Alignment() int { return b.alignment }
func (b *DirectBackend) Name() string   { return "direct" }
func (b *DirectBackend) Close() error   { return unix.Close(b.fd) }

func (b *DirectBackend) scratchBuf(size int) []byte {
	if len(b.scratch) >= size {
		return b.scratch[:size]
	}
	b.scratch = alignedBuffer(size, b.alignment)
	return b.scratch
}

// alignedBuffer allocates a byte slice whose start address is a multiple of
// align — O_DIRECT requires the user-space buffer itself to be memory-
// aligned, not just the file offset/length.
func alignedBuffer(size, align int) []byte {
	if align <= 1 {
		return make([]byte, size)
	}
	buf := make([]byte, size+align-1)
	addr := uintptr(unsafe.Pointer(&buf[0]))
	pad := (align - int(addr%uintptr(align))) % align
	return buf[pad : pad+size]
}
