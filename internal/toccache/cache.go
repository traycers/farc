// Package toccache is hls_server's persistent on-disk cache of TOC bytes,
// keyed by (storage id, fcontainer UUID) -- see
// .scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md.
// It has no eviction policy of its own: the catalog-diff bootstrap in
// internal/tocindex decides when an entry is stale and calls Delete
// explicitly, the same way internal/segmentcache's disk backend leaves
// quota/LRU bookkeeping to its own Cache type rather than to diskBackend.
package toccache

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// uuidFromHexName is pathFor's inverse for one path component -- the file
// name List reads back into a [16]byte, skipping anything that isn't a
// valid 16-byte hex name (defensive against stray files under a storage's
// subdirectory).
func uuidFromHexName(name string) ([16]byte, bool) {
	var uuid [16]byte
	b, err := hex.DecodeString(name)
	if err != nil || len(b) != 16 {
		return uuid, false
	}
	copy(uuid[:], b)
	return uuid, true
}

// Cache is a plain local-filesystem store: dir/storageID/uuidHex holds
// exactly the bytes a caller Put for that key.
type Cache struct {
	dir string
}

// New returns a Cache rooted at dir, creating dir if it doesn't exist yet.
func New(dir string) (*Cache, error) {
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return nil, fmt.Errorf("toccache: create cache dir: %w", err)
	}
	return &Cache{dir: dir}, nil
}

func (c *Cache) pathFor(storageID string, uuid [16]byte) string {
	return filepath.Join(c.dir, storageID, hex.EncodeToString(uuid[:]))
}

// Get returns the bytes previously Put for (storageID, uuid), or
// ok == false if nothing was, or the entry was Deleted.
func (c *Cache) Get(storageID string, uuid [16]byte) (data []byte, ok bool) {
	data, err := os.ReadFile(c.pathFor(storageID, uuid))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put stores data under (storageID, uuid), overwriting any previous entry.
func (c *Cache) Put(storageID string, uuid [16]byte, data []byte) error {
	path := c.pathFor(storageID, uuid)
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		return fmt.Errorf("toccache: create storage dir: %w", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		return fmt.Errorf("toccache: write entry: %w", err)
	}
	return nil
}

// Delete removes the entry for (storageID, uuid), if any. A missing entry
// is not an error -- callers evict on catalog-diff, which naturally retries
// uuids that were never cached in the first place.
func (c *Cache) Delete(storageID string, uuid [16]byte) {
	_ = os.Remove(c.pathFor(storageID, uuid))
}

// List returns every uuid currently cached for storageID, in no particular
// order.
func (c *Cache) List(storageID string) ([][16]byte, error) {
	entries, err := os.ReadDir(filepath.Join(c.dir, storageID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("toccache: list storage dir: %w", err)
	}
	var out [][16]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		uuid, ok := uuidFromHexName(e.Name())
		if !ok {
			continue
		}
		out = append(out, uuid)
	}
	return out, nil
}
