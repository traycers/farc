package api

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/traycers/farc/internal/storage"
)

func u32le(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func TestHandleGetFblock(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fblocks/%d", srv.URL, idx))
	if err != nil {
		t.Fatalf("GET fblock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var info fblockInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.State != "ready" {
		t.Errorf("State = %q, want %q", info.State, "ready")
	}
	if info.UUID != hex.EncodeToString(uuid[:]) {
		t.Errorf("UUID = %q, want %q", info.UUID, hex.EncodeToString(uuid[:]))
	}
}

func TestHandleGetFblock_OutOfRange(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks/999999")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
