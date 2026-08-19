package toccache_test

import (
	"testing"

	"github.com/traycers/farc/internal/toccache"
)

// TestCache_PutThenGet is toccache's core contract
// (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// a TOC blob written for one (storage, uuid) key comes back byte-identical,
// and a key nothing was ever written for is reported as absent rather than
// as empty bytes.
func TestCache_PutThenGet(t *testing.T) {
	dir := t.TempDir()
	c, err := toccache.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	uuid := [16]byte{1, 2, 3, 4}
	want := []byte("toc-bytes-for-uuid")

	if err := c.Put("s1", uuid, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := c.Get("s1", uuid)
	if !ok {
		t.Fatalf("Get after Put: ok = false, want true")
	}
	if string(got) != string(want) {
		t.Fatalf("Get after Put = %q, want %q", got, want)
	}

	other := [16]byte{9, 9, 9}
	if _, ok := c.Get("s1", other); ok {
		t.Fatalf("Get for a uuid never Put: ok = true, want false")
	}
}

// TestCache_DeleteAndList is the second half of toccache's contract: the
// diff-bootstrap layer (issue 02) needs to enumerate what's on disk for a
// storage (List) and drop entries whose uuid has aged out of farcd's
// catalog (Delete) -- both cross storage boundaries correctly, since two
// storages can otherwise collide on uuid alone.
func TestCache_DeleteAndList(t *testing.T) {
	dir := t.TempDir()
	c, err := toccache.New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u1 := [16]byte{1}
	u2 := [16]byte{2}
	u3 := [16]byte{3}
	if err := c.Put("s1", u1, []byte("a")); err != nil {
		t.Fatalf("Put u1: %v", err)
	}
	if err := c.Put("s1", u2, []byte("b")); err != nil {
		t.Fatalf("Put u2: %v", err)
	}
	if err := c.Put("s2", u3, []byte("c")); err != nil {
		t.Fatalf("Put u3 (other storage): %v", err)
	}

	list, err := c.List("s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[[16]byte]bool{}
	for _, u := range list {
		got[u] = true
	}
	if len(got) != 2 || !got[u1] || !got[u2] {
		t.Fatalf("List(s1) = %v, want exactly {u1, u2}", list)
	}

	c.Delete("s1", u1)
	if _, ok := c.Get("s1", u1); ok {
		t.Fatalf("Get after Delete: ok = true, want false")
	}

	list, err = c.List("s1")
	if err != nil {
		t.Fatalf("List after Delete: %v", err)
	}
	if len(list) != 1 || list[0] != u2 {
		t.Fatalf("List(s1) after deleting u1 = %v, want exactly {u2}", list)
	}
}
