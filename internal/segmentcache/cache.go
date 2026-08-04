// Package segmentcache is hls_server's disk-backed cache of already-built
// segments (per the earlier design discussion: disk-only, no in-memory
// cache), quota-bounded with LRU eviction. It never talks to farcd or
// rebuilds a segment itself — internal/hlsapi's job is to check Get, fall
// back to internal/segment on a miss, and Put the result.
package segmentcache

import (
	"container/list"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type entry struct {
	key  Key
	path string
	size int64
}

// Cache is a quota-bounded, LRU-evicted disk cache of segment bytes, keyed
// by Key. Safe for concurrent use.
type Cache struct {
	dir   string
	quota int64 // <= 0 means unbounded (no eviction)

	mu      sync.Mutex
	entries map[Key]*list.Element // -> *entry, list.Front() == most recently used
	order   *list.List
	size    int64
}

// New opens (creating if necessary) a disk cache rooted at dir, bounded by
// quotaBytes (<= 0 for unbounded). Any files already under dir from a prior
// run are indexed (oldest mtime first) and count against the quota
// immediately — including evicting down to quota right away, if dir already
// holds more than quotaBytes (e.g. quotaBytes was lowered since the last
// run).
func New(dir string, quotaBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("segmentcache: create cache dir: %w", err)
	}
	c := &Cache{
		dir:     dir,
		quota:   quotaBytes,
		entries: make(map[Key]*list.Element),
		order:   list.New(),
	}
	if err := c.loadExisting(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.evictToQuotaLocked()
	c.mu.Unlock()
	return c, nil
}

// Size returns the cache's current total size in bytes.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Get returns k's cached bytes, if present, marking it most recently used.
// A file that vanished from disk outside the cache's own control is treated
// as a miss and its stale entry is dropped.
func (c *Cache) Get(k Key) ([]byte, bool) {
	c.mu.Lock()
	el, ok := c.entries[k]
	if !ok {
		c.mu.Unlock()
		return nil, false
	}
	c.order.MoveToFront(el)
	path := el.Value.(*entry).path
	c.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		c.mu.Lock()
		c.removeEntryLocked(k)
		c.mu.Unlock()
		return nil, false
	}
	return data, true
}

// Put writes data under k, evicting least-recently-used entries afterward
// if the cache is now over quota.
func (c *Cache) Put(k Key, data []byte) error {
	path := c.pathFor(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("segmentcache: create entry dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("segmentcache: write entry: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[k]; ok {
		e := el.Value.(*entry)
		c.size += int64(len(data)) - e.size
		e.size = int64(len(data))
		c.order.MoveToFront(el)
	} else {
		e := &entry{key: k, path: path, size: int64(len(data))}
		c.entries[k] = c.order.PushFront(e)
		c.size += e.size
	}
	c.evictToQuotaLocked()
	return nil
}

func (c *Cache) removeEntryLocked(k Key) {
	el, ok := c.entries[k]
	if !ok {
		return
	}
	e := el.Value.(*entry)
	c.order.Remove(el)
	delete(c.entries, k)
	c.size -= e.size
}

// evictToQuotaLocked drops least-recently-used entries (back of the list)
// until the cache is at or under quota. mu must be held.
func (c *Cache) evictToQuotaLocked() {
	if c.quota <= 0 {
		return
	}
	for c.size > c.quota {
		back := c.order.Back()
		if back == nil {
			return
		}
		e := back.Value.(*entry)
		c.order.Remove(back)
		delete(c.entries, e.key)
		c.size -= e.size
		_ = os.Remove(e.path)
	}
}

// pathFor maps a Key to its on-disk path: dir/storageID/uuidHex/name.
func (c *Cache) pathFor(k Key) string {
	return filepath.Join(c.dir, k.StorageID, hex.EncodeToString(k.UUID[:]), fileName(k))
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
	if len(parts) != 3 {
		return Key{}, false
	}
	uuidBytes, err := hex.DecodeString(parts[1])
	if err != nil || len(uuidBytes) != 16 {
		return Key{}, false
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)

	if parts[2] == "init.mp4" {
		return InitKey(parts[0], uuid), true
	}
	n, err := strconv.Atoi(strings.TrimSuffix(parts[2], ".m4s"))
	if err != nil {
		return Key{}, false
	}
	return MediaKey(parts[0], uuid, n), true
}

// loadExisting rebuilds the in-memory index from whatever's already on disk
// under c.dir, ordered oldest-mtime-first so evictToQuotaLocked (if the
// directory already exceeds quota) drops the actual least-recently-written
// files first.
func (c *Cache) loadExisting() error {
	type found struct {
		key     Key
		path    string
		size    int64
		modTime int64
	}
	var files []found

	err := filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(c.dir, path)
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
		files = append(files, found{key: key, path: path, size: info.Size(), modTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("segmentcache: scan existing cache dir: %w", err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].modTime < files[j].modTime })
	for _, f := range files {
		e := &entry{key: f.key, path: f.path, size: f.size}
		c.entries[f.key] = c.order.PushFront(e)
		c.size += f.size
	}
	return nil
}
