package segmentcache_test

import (
	"bytes"
	"testing"
	"time"

	"traycers/farc/internal/segmentcache"
)

func TestCache_PutGetRoundTrip(t *testing.T) {
	c, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	key := segmentcache.InitKey(1, "s1", [16]byte{1})
	if err := c.Put(key, []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get(key)
	if !ok || !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("Get() = (%q, %v), want (\"hello\", true)", got, ok)
	}
}

func TestCache_GetMiss(t *testing.T) {
	c, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := c.Get(segmentcache.MediaKey(1, "s1", [16]byte{9}, 0)); ok {
		t.Fatalf("Get() on an empty cache = found, want miss")
	}
}

func TestCache_EvictsLeastRecentlyUsedUnderQuota(t *testing.T) {
	dir := t.TempDir()
	// quota fits exactly two 10-byte entries.
	c, err := segmentcache.New(dir, 20)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := segmentcache.MediaKey(1, "s1", [16]byte{1}, 0)
	b := segmentcache.MediaKey(1, "s1", [16]byte{2}, 0)
	d := segmentcache.MediaKey(1, "s1", [16]byte{3}, 0)

	mustPut(t, c, a, 10)
	mustPut(t, c, b, 10)

	// Touch a so it becomes more recently used than b.
	if _, ok := c.Get(a); !ok {
		t.Fatalf("Get(a) = miss before eviction")
	}

	// A third entry pushes size to 30 > quota(20); b (least recently used)
	// must be evicted, not a.
	mustPut(t, c, d, 10)

	if _, ok := c.Get(a); !ok {
		t.Fatalf("Get(a) after eviction = miss, want hit (a was recently used)")
	}
	if _, ok := c.Get(b); ok {
		t.Fatalf("Get(b) after eviction = hit, want miss (b was least recently used)")
	}
	if _, ok := c.Get(d); !ok {
		t.Fatalf("Get(d) after eviction = miss, want hit (just inserted)")
	}
	if got := c.Size(); got != 20 {
		t.Fatalf("Size() = %d, want 20", got)
	}
}

func TestCache_SameKeyFromDifferentWindowsHitsSameEntry(t *testing.T) {
	// The exact caching payoff ADR-019 is justified by: two different
	// playback windows covering the same fcontainer resolve to the same
	// (storage,uuid,segIndex) key.
	c, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	uuid := [16]byte{7}
	keyFromWindowA := segmentcache.MediaKey(5, "s1", uuid, 2)
	keyFromWindowB := segmentcache.MediaKey(5, "s1", uuid, 2)
	if keyFromWindowA != keyFromWindowB {
		t.Fatalf("keys from two windows over the same fcontainer differ: %+v != %+v", keyFromWindowA, keyFromWindowB)
	}

	if err := c.Put(keyFromWindowA, []byte("segment-bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get(keyFromWindowB)
	if !ok || string(got) != "segment-bytes" {
		t.Fatalf("Get(keyFromWindowB) = (%q, %v), want (\"segment-bytes\", true)", got, ok)
	}
}

func TestCache_DifferentChannelsSameUUIDDoNotCollide(t *testing.T) {
	// One fcontainer routinely holds several channels' interleaved data at
	// once (ADR-014) — Channel must be part of the key, or one channel's
	// player would be served another channel's init/media segment.
	c, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	uuid := [16]byte{9}
	channelA := segmentcache.InitKey(1, "s1", uuid)
	channelB := segmentcache.InitKey(2, "s1", uuid)

	if err := c.Put(channelA, []byte("channel-1-init")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := c.Get(channelB); ok {
		t.Fatalf("Get(channelB) = hit, want miss (channel 1's cached init segment must not be served for channel 2)")
	}

	if err := c.Put(channelB, []byte("channel-2-init")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	gotA, ok := c.Get(channelA)
	if !ok || string(gotA) != "channel-1-init" {
		t.Fatalf("Get(channelA) = (%q, %v), want (\"channel-1-init\", true)", gotA, ok)
	}
	gotB, ok := c.Get(channelB)
	if !ok || string(gotB) != "channel-2-init" {
		t.Fatalf("Get(channelB) = (%q, %v), want (\"channel-2-init\", true)", gotB, ok)
	}
}

func TestCache_ReloadsExistingFilesFromDisk(t *testing.T) {
	dir := t.TempDir()
	c1, err := segmentcache.New(dir, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	key := segmentcache.InitKey(1, "s1", [16]byte{4})
	if err := c1.Put(key, []byte("persisted")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	c2, err := segmentcache.New(dir, 0)
	if err != nil {
		t.Fatalf("New (reopen): %v", err)
	}
	got, ok := c2.Get(key)
	if !ok || string(got) != "persisted" {
		t.Fatalf("Get() after reopen = (%q, %v), want (\"persisted\", true)", got, ok)
	}
	if got := c2.Size(); got != int64(len("persisted")) {
		t.Fatalf("Size() after reopen = %d, want %d", got, len("persisted"))
	}
}

func TestCache_ReopenEvictsDownToLoweredQuota(t *testing.T) {
	dir := t.TempDir()
	c1, err := segmentcache.New(dir, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	older := segmentcache.MediaKey(1, "s1", [16]byte{1}, 0)
	newer := segmentcache.MediaKey(1, "s1", [16]byte{2}, 0)
	mustPut(t, c1, older, 10)
	time.Sleep(5 * time.Millisecond) // ensure a distinct, later mtime
	mustPut(t, c1, newer, 10)

	c2, err := segmentcache.New(dir, 10) // quota now only fits one entry
	if err != nil {
		t.Fatalf("New (reopen with lower quota): %v", err)
	}
	if _, ok := c2.Get(older); ok {
		t.Fatalf("Get(older) after reopen with lowered quota = hit, want evicted")
	}
	if _, ok := c2.Get(newer); !ok {
		t.Fatalf("Get(newer) after reopen with lowered quota = miss, want hit")
	}
}

func mustPut(t *testing.T, c *segmentcache.Cache, key segmentcache.Key, size int) {
	t.Helper()
	if err := c.Put(key, bytes.Repeat([]byte{0xAB}, size)); err != nil {
		t.Fatalf("Put(%+v): %v", key, err)
	}
}
