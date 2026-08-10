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
	var out []fblockInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty fblocks list")
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
	var out []fblockInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, fb := range out {
		if fb.Index == idx {
			if !fb.Protected {
				t.Fatalf("fblock %d Protected = false, want true", idx)
			}
			return
		}
	}
	t.Fatalf("fblock %d not found in list: %+v", idx, out)
}
