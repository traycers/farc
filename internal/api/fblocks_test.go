package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
)

func TestHandleListFblocks(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "hello-frame", 150, 1000)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks")
	if err != nil {
		t.Fatalf("GET /fblocks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body fblockListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := body.Fblocks
	if len(out) == 0 {
		t.Fatal("empty fblocks list")
	}
	if body.Total != len(out) {
		t.Fatalf("total = %d, want %d (no paging requested)", body.Total, len(out))
	}

	var ready *fblockInfo
	for i := range out {
		if out[i].State == "ready" {
			ready = &out[i]
		} else if out[i].UUID != "" || out[i].Begin != 0 || out[i].End != 0 || len(out[i].Channels) != 0 {
			t.Fatalf("non-ready entry %+v carries Ready-only fields", out[i])
		}
	}
	if ready == nil {
		t.Fatal("no ready fblock in list")
	}
	if ready.UUID != hex.EncodeToString(uuid[:]) {
		t.Fatalf("ready.UUID = %q, want %q", ready.UUID, hex.EncodeToString(uuid[:]))
	}
	if ready.Begin != 100 || ready.End != 200 {
		t.Fatalf("ready begin/end = %d/%d, want 100/200", ready.Begin, ready.End)
	}
	if len(ready.Channels) != 1 || ready.Channels[0] != 1 {
		t.Fatalf("ready.Channels = %v, want [1]", ready.Channels)
	}
	if ready.Protected {
		t.Fatalf("ready.Protected = true, want false")
	}
}

func TestHandleListFblocks_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	srv := newTestServer(t, reg)
	resp, err := http.Get(srv.URL + "/storages/nope/fblocks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleListFblocks_ProtectedFlagReported(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "hello-frame", 150, 1000)
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatal("ResolveUUID: fblock just written not found")
	}
	err := u.Index().SetProtected(idx, true)
	if err != nil {
		t.Fatalf("SetProtected: %v", err)
	}
	err = reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks")
	if err != nil {
		t.Fatalf("GET /fblocks: %v", err)
	}
	defer resp.Body.Close()
	var body fblockListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, fb := range body.Fblocks {
		if fb.Index == idx {
			if !fb.Protected {
				t.Fatalf("fblock %d Protected = false, want true", idx)
			}
			return
		}
	}
	t.Fatalf("fblock %d not found in list: %+v", idx, body.Fblocks)
}

func TestHandleListFblocks_Paging(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t) // newTestUnit's storage has Geometry.N == 4, see testutil_test.go.
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks?offset=1&limit=2")
	if err != nil {
		t.Fatalf("GET /fblocks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body fblockListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 4 {
		t.Fatalf("total = %d, want 4", body.Total)
	}
	if len(body.Fblocks) != 2 {
		t.Fatalf("len(fblocks) = %d, want 2", len(body.Fblocks))
	}
	if body.Fblocks[0].Index != 1 || body.Fblocks[1].Index != 2 {
		t.Fatalf("fblocks indices = [%d, %d], want [1, 2]", body.Fblocks[0].Index, body.Fblocks[1].Index)
	}

	resp2, err := http.Get(srv.URL + "/storages/s1/fblocks?offset=0&limit=0")
	if err != nil {
		t.Fatalf("GET /fblocks: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for limit=0", resp2.StatusCode)
	}
}
