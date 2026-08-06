// Package segmentcache is hls_server's cache of already-built segments. A
// disk-backed Cache (New) is quota-bounded with LRU eviction, one process
// per directory. An object-storage-backed Cache (NewS3) has no local disk to
// bound and no per-process state at all — every hls_server replica reads and
// writes the same bucket, so there's nothing to evict or warm-start; space
// bounding for that backend is a store-side lifecycle policy, not this
// package's job. Either way, this package never talks to farcd or rebuilds a
// segment itself — internal/hlsapi's job is to check Get, fall back to
// internal/segment on a miss, and Put the result.
package segmentcache

import (
	"container/list"
	"sync"
)

// backend is the low-level byte store a Cache delegates actual reads/writes
// to. diskBackend (this package) is today's plain local-filesystem store;
// s3Backend (s3.go) talks to an S3-compatible object store instead — the Go
// code only depends on the S3 API, not on any specific product, so which
// server actually backs it (SeaweedFS, MinIO, AWS S3, Ceph RGW, ...) is a
// pure deployment choice.
type backend interface {
	get(k Key) ([]byte, bool)
	put(k Key, data []byte) error
	delete(k Key)
}

type entry struct {
	key  Key
	size int64
}

// Cache is a cache of segment bytes, keyed by Key. Safe for concurrent use.
type Cache struct {
	backend backend

	// trackLRU is true only for a disk-backed Cache (New): object storage
	// (NewS3) skips this bookkeeping entirely rather than accumulating an
	// ever-growing, never-evicted entries map for a backend that has no
	// local disk to bound in the first place.
	trackLRU bool
	quota    int64 // <= 0 means unbounded (no eviction); meaningful only when trackLRU

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
	db, err := newDiskBackend(dir)
	if err != nil {
		return nil, err
	}
	c := &Cache{
		backend:  db,
		trackLRU: true,
		quota:    quotaBytes,
		entries:  make(map[Key]*list.Element),
		order:    list.New(),
	}
	err = c.loadExisting(db)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.evictToQuotaLocked()
	c.mu.Unlock()
	return c, nil
}

// NewS3 creates an object-storage-backed Cache against client/bucket. Unlike
// New, there is no local warm-start (the bucket already holds everything
// every replica needs) and no quota/eviction (space bounding is the
// bucket's own lifecycle policy).
func NewS3(client s3API, bucket string) *Cache {
	return &Cache{backend: newS3Backend(client, bucket)}
}

// Size returns the cache's current total size in bytes. Only meaningful for
// a disk-backed Cache (New) — an object-storage-backed Cache (NewS3) never
// tracks this and always reports 0.
func (c *Cache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Get returns k's cached bytes, if present, marking it most recently used.
// A file that vanished outside the cache's own control is treated as a miss
// and its stale entry is dropped.
func (c *Cache) Get(k Key) ([]byte, bool) {
	if !c.trackLRU {
		return c.backend.get(k)
	}

	c.mu.Lock()
	el, ok := c.entries[k]
	if !ok {
		c.mu.Unlock()
		return nil, false
	}
	c.order.MoveToFront(el)
	c.mu.Unlock()

	data, ok := c.backend.get(k)
	if !ok {
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
	if !c.trackLRU {
		return c.backend.put(k, data)
	}

	err := c.backend.put(k, data)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[k]; ok {
		e := el.Value.(*entry)
		c.size += int64(len(data)) - e.size
		e.size = int64(len(data))
		c.order.MoveToFront(el)
	} else {
		e := &entry{key: k, size: int64(len(data))}
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
		c.backend.delete(e.key)
	}
}

// loadExisting rebuilds the in-memory index from whatever's already on disk
// under db, ordered oldest-mtime-first so evictToQuotaLocked (if the
// directory already exceeds quota) drops the actual least-recently-written
// files first.
func (c *Cache) loadExisting(db *diskBackend) error {
	files, err := db.walk()
	if err != nil {
		return err
	}
	for _, f := range files {
		e := &entry{key: f.key, size: f.size}
		c.entries[f.key] = c.order.PushFront(e)
		c.size += f.size
	}
	return nil
}
