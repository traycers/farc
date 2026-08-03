package ioengine

import "os"

// StandardBackend wraps a plain os.File. Portable (works on any platform
// and any filesystem, including tmpfs), but its write-verify guarantee is
// degraded relative to `direct`: ReadAt may be served from the OS page
// cache, so a successful write+ReadAt+compare only proves the data reached
// the OS, not the physical media (ADR-010).
type StandardBackend struct {
	f *os.File
}

// OpenStandard opens path with the standard backend. flag/perm follow
// os.OpenFile conventions (e.g. os.O_RDWR|os.O_CREATE).
func OpenStandard(path string, flag int, perm os.FileMode) (*StandardBackend, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return &StandardBackend{f: f}, nil
}

func (b *StandardBackend) ReadAt(p []byte, offset int64) (int, error) {
	return b.f.ReadAt(p, offset)
}

func (b *StandardBackend) WriteAt(p []byte, offset int64) (int, error) {
	return b.f.WriteAt(p, offset)
}

func (b *StandardBackend) Sync() error {
	return b.f.Sync()
}

func (b *StandardBackend) Alignment() int {
	return 1
}

func (b *StandardBackend) Name() string {
	return "standard"
}

func (b *StandardBackend) Close() error {
	return b.f.Close()
}
