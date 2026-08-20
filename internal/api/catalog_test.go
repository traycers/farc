package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/traycers/farc/internal/storage"
)

// TestHandleGetCatalog is the bulk-catalog endpoint's core contract
// (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// one request returns every fblock's state, and Ready ones additionally
// carry the uuid/begin/end a diff-based bootstrap needs -- no per-index
// round trips, no channel argument (decided 2026-08-13: unfiltered).
func TestHandleGetCatalog(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "hello-frame", 150, 1000)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}

	resp, err := http.Get(srv.URL + "/storages/s1/catalog")
	if err != nil {
		t.Fatalf("GET catalog: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var entries []catalogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found *catalogEntry
	for i := range entries {
		if entries[i].Index == idx {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("catalog entries = %+v, want an entry for index %d", entries, idx)
	}
	if found.State != "ready" {
		t.Errorf("State = %q, want %q", found.State, "ready")
	}
	if found.UUID != hex.EncodeToString(uuid[:]) {
		t.Errorf("UUID = %q, want %q", found.UUID, hex.EncodeToString(uuid[:]))
	}
	if found.Begin != "100" || found.End != "200" {
		t.Errorf("Begin/End = %q/%q, want 100/200", found.Begin, found.End)
	}
}

// TestHandleGetCatalog_IncludesProtectedFlag proves the bulk catalog carries
// the protected flag per fblock -- the fblocks-status-grid page (web) needs
// it to draw the purple "protected" border live, and re-fetching every
// index individually via GET .../fblocks/{index} just to learn this one
// flag would defeat the bulk endpoint's whole point.
func TestHandleGetCatalog_IncludesProtectedFlag(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "hello-frame", 150, 1000)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}
	if err := u.Index().SetProtected(idx, true); err != nil {
		t.Fatalf("SetProtected: %v", err)
	}

	resp, err := http.Get(srv.URL + "/storages/s1/catalog")
	if err != nil {
		t.Fatalf("GET catalog: %v", err)
	}
	defer resp.Body.Close()
	var entries []catalogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found *catalogEntry
	for i := range entries {
		if entries[i].Index == idx {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("catalog entries = %+v, want an entry for index %d", entries, idx)
	}
	if !found.Protected {
		t.Errorf("Protected = false, want true")
	}
}

func TestHandleGetCatalog_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/nope/catalog")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
