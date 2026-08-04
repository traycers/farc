package storage

import (
	"fmt"
	"os"
)

// Geometry is a Storage's fixed, init-time shape (docs/docs/archive/
// 04-storage-operations.md §3.1 step 3). FblockSize and MaxChannels never
// change without a dedicated expand/shrink operation — out of v1 scope,
// GeometryManager deferred alongside JobRunner.
type Geometry struct {
	FblockSize  uint64
	N           uint32
	MaxChannels uint16
}

// CreateSizedFile creates (or truncates) path to exactly size bytes — step
// 2's file-mode branch ("создать файл нужного размера"). Block-device mode
// (sizing done by the operator at the OS level) is out of scope here.
func CreateSizedFile(path string, size int64, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, perm)
	if err != nil {
		return fmt.Errorf("storage: create %s: %w", path, err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("storage: truncate %s to %d: %w", path, size, err)
	}
	return nil
}
