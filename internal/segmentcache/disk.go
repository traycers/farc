package segmentcache

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// diskBackend is a plain local-filesystem backend: dir/channel/storageID/
// uuidHex/{init.mp4|N.m4s}. All of Cache's LRU/quota bookkeeping lives in
// cache.go; this type only knows how to turn a Key into bytes on disk.
type diskBackend struct {
	dir string
}

func newDiskBackend(dir string) (*diskBackend, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("segmentcache: create cache dir: %w", err)
	}
	return &diskBackend{dir: dir}, nil
}

func (b *diskBackend) get(k Key) ([]byte, bool) {
	data, err := os.ReadFile(b.pathFor(k))
	if err != nil {
		return nil, false
	}
	return data, true
}

func (b *diskBackend) put(k Key, data []byte) error {
	path := b.pathFor(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("segmentcache: create entry dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("segmentcache: write entry: %w", err)
	}
	return nil
}

func (b *diskBackend) delete(k Key) {
	_ = os.Remove(b.pathFor(k))
}

// pathFor maps a Key to its on-disk path: dir/channel/storageID/uuidHex/name.
func (b *diskBackend) pathFor(k Key) string {
	return filepath.Join(b.dir, strconv.Itoa(int(k.Channel)), k.StorageID, hex.EncodeToString(k.UUID[:]), fileName(k))
}

func fileName(k Key) string {
	if k.IsInit() {
		return "init.mp4"
	}
	return strconv.Itoa(k.SegIndex) + ".m4s"
}

// parseKeyFromRelPath is pathFor's inverse, given a path relative to dir.
func parseKeyFromRelPath(rel string) (Key, bool) {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 {
		return Key{}, false
	}
	channel, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return Key{}, false
	}
	uuidBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(uuidBytes) != 16 {
		return Key{}, false
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)

	if parts[3] == "init.mp4" {
		return InitKey(uint16(channel), parts[1], uuid), true
	}
	n, err := strconv.Atoi(strings.TrimSuffix(parts[3], ".m4s"))
	if err != nil {
		return Key{}, false
	}
	return MediaKey(uint16(channel), parts[1], uuid, n), true
}

type foundFile struct {
	key  Key
	size int64
}

// walk scans b.dir for already-existing entries, oldest-mtime-first — the
// order New's loadExisting needs so evictToQuotaLocked (if the directory
// already exceeds quota) drops the actual least-recently-written files
// first.
func (b *diskBackend) walk() ([]foundFile, error) {
	type found struct {
		key     Key
		size    int64
		modTime int64
	}
	var files []found

	err := filepath.WalkDir(b.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.dir, path)
		if err != nil {
			return nil
		}
		key, ok := parseKeyFromRelPath(rel)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, found{key: key, size: info.Size(), modTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("segmentcache: scan existing cache dir: %w", err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime < files[j].modTime })
	out := make([]foundFile, len(files))
	for i, f := range files {
		out[i] = foundFile{key: f.key, size: f.size}
	}
	return out, nil
}
