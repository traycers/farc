package api

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// unknownUUIDHex is a UUID guaranteed not to resolve: unlike an all-zero
// UUID, which collides with fblock 0's own placeholder identity (Init never
// assigns fblock 0 a real UUID, so it stays the zero value and, once
// promoted to Ready, resolves via ResolveUUID like any other fcontainer
// would), all-0xff never occurs (real fcontainers get crypto/rand UUIDv4s).
const unknownUUIDHex = "ffffffffffffffffffffffffffffffff"

func newTestServer(t *testing.T, reg *StorageRegistry) *httptest.Server {
	t.Helper()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleReadTOC(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s/toc", srv.URL, hex.EncodeToString(uuid[:])))
	if err != nil {
		t.Fatalf("GET toc: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	columns, err := toc.Decode(buf)
	if err != nil {
		t.Fatalf("toc.Decode: %v", err)
	}
	if len(toc.ScanByRole(columns, mediatree.RoleChannel)) != 1 {
		t.Fatalf("expected exactly one channel node in TOC")
	}
}

func TestHandleReadTOC_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	srv := newTestServer(t, reg)
	resp, err := http.Get(srv.URL + "/storages/nope/fcontainers/" + unknownUUIDHex + "/toc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleReadContent_WholeExport(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s", srv.URL, hex.EncodeToString(uuid[:])))
	if err != nil {
		t.Fatalf("GET content: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	size, err := u.ContentSize(uuid)
	if err != nil {
		t.Fatalf("ContentSize: %v", err)
	}
	if int64(len(buf)) != size {
		t.Fatalf("body len = %d, want ContentSize %d", len(buf), size)
	}
	// The Content section is zero-padded out to its full physical capacity
	// (internal/storage's own documented gap-fix #1), so a whole export
	// can't be fed straight into mediatree.DecodeContent -- trailing
	// padding isn't a clean sequence of elements. A real consumer uses the
	// TOC (as TestHandleReadContent_Ranges does) to know exactly where
	// each node's bytes are; this just checks the raw bytes are in there.
	if !bytes.Contains(buf, []byte("hello-frame")) {
		t.Fatalf("whole export does not contain the written frame bytes")
	}
}

func TestHandleReadContent_Ranges(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	// Read the whole thing once to know the frame_data node's real offset.
	columns, err := u.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	ids := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(ids) != 1 {
		t.Fatalf("expected 1 frame_data(video) node, got %d", len(ids))
	}
	offset, size, ok := toc.ContentOffset(columns, ids[0])
	if !ok {
		t.Fatalf("ContentOffset: not variable-width")
	}

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s?ranges=%d:%d", srv.URL, hex.EncodeToString(uuid[:]), offset, size))
	if err != nil {
		t.Fatalf("GET ranged content: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(buf) != "hello-frame" {
		t.Fatalf("body = %q, want %q", buf, "hello-frame")
	}
}

func TestHandleSetProtected(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp := postJSON(t, srv, fmt.Sprintf("/storages/s1/fcontainers/%s/protected", hex.EncodeToString(uuid[:])), setProtectedRequest{Value: true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}
	if !u.Index().Snapshot().Protected(idx) {
		t.Fatalf("fblock %d not protected after POST", idx)
	}
}

func TestHandleSetProtected_UnknownUUID(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp := postJSON(t, srv, "/storages/s1/fcontainers/"+unknownUUIDHex+"/protected", setProtectedRequest{Value: true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
