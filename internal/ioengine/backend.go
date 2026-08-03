// Package ioengine implements IoBackend (ADR-010): the thin wrapper over a
// file or block device that is the only component making disk syscalls
// (docs/docs/archive/02-storage.md §4.2.5). Two implementations exist behind
// one interface: `direct` (Linux, O_DIRECT, bypasses the page cache so
// write-verify readback is honest) and `standard` (portable os.File,
// documented degraded verify guarantee).
package ioengine

import "errors"

// Backend is IoBackend: "записать N байт по смещению", "прочитать M байт по
// смещению" — nothing else. It does not know fblock/fchunk structure; all
// offsets and lengths are given by the caller (StorageEngine).
type Backend interface {
	// ReadAt reads len(p) bytes starting at offset into p.
	ReadAt(p []byte, offset int64) (n int, err error)
	// WriteAt writes p at offset.
	WriteAt(p []byte, offset int64) (n int, err error)
	// Sync flushes previously written data to stable storage
	// (fdatasync/File.Sync) — must be called before a write is considered
	// durable (docs/docs/archive/adr/010-direct-io.md).
	Sync() error
	// Alignment is the required buffer/offset/length alignment in bytes:
	// the device's logical block size for `direct`, 1 (no requirement) for
	// `standard`.
	Alignment() int
	// Name identifies the backend for metrics/startup logs ("direct" or
	// "standard").
	Name() string
	Close() error
}

// ErrMisaligned is returned by a `direct` backend's WriteAt when offset or
// len(p) is not a multiple of Alignment() — physical writes must already be
// alignment-sized pieces by the time they reach IoBackend (ADR-017: the
// caller, not this package, is responsible for producing aligned writes).
var ErrMisaligned = errors.New("ioengine: offset/length not aligned to required block size")

func isAligned(v int64, align int) bool {
	return align <= 1 || v%int64(align) == 0
}

// roundRange rounds [offset, offset+length) outward to align-byte
// boundaries. skip is where the originally requested range starts within a
// buffer of size alignedLength read from alignedOffset — the
// backend-internal mechanism a `direct` ReadAt uses to satisfy arbitrary
// (unaligned) read requests, invisible to the caller (ADR-010).
func roundRange(offset, length int64, align int) (alignedOffset, alignedLength, skip int64) {
	a := int64(align)
	if a <= 1 {
		return offset, length, 0
	}
	alignedOffset = offset - (offset % a)
	end := offset + length
	rem := end % a
	alignedEnd := end
	if rem != 0 {
		alignedEnd = end + (a - rem)
	}
	return alignedOffset, alignedEnd - alignedOffset, offset - alignedOffset
}
