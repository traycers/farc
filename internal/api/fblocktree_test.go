package api

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/traycers/farc/mediatree"
)

func u32le(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func TestFormatNodeValue(t *testing.T) {
	f32 := make([]byte, 4)
	binary.LittleEndian.PutUint32(f32, math.Float32bits(1.5))
	f64 := make([]byte, 8)
	binary.LittleEndian.PutUint64(f64, math.Float64bits(2.5))
	var negI32 int32 = -7
	i32 := make([]byte, 4)
	binary.LittleEndian.PutUint32(i32, uint32(negI32))
	var negI64 int64 = -9
	i64 := make([]byte, 8)
	binary.LittleEndian.PutUint64(i64, uint64(negI64))

	cases := []struct {
		t    mediatree.NodeType
		raw  []byte
		want string
	}{
		{mediatree.TypeVoid, nil, ""},
		{mediatree.TypeUint8, []byte{7}, "7"},
		{mediatree.TypeUint32, u32le(42), "42"},
		{mediatree.TypeUint64, []byte{1, 0, 0, 0, 0, 0, 0, 0}, "1"},
		{mediatree.TypeInt32, i32, "-7"},
		{mediatree.TypeInt64, i64, "-9"},
		{mediatree.TypeFloat32, f32, "1.5"},
		{mediatree.TypeFloat64, f64, "2.5"},
		{mediatree.TypeTimestamp, []byte{0, 0, 0, 0, 1, 0, 0, 0}, "4294967296"},
		{mediatree.TypeString, []byte("hi"), ""},
		{mediatree.TypeBytes, []byte("hi"), ""},
	}
	for _, c := range cases {
		if got := formatNodeValue(c.t, c.raw); got != c.want {
			t.Errorf("formatNodeValue(%v, %v) = %q, want %q", c.t, c.raw, got, c.want)
		}
	}
}

func TestHandleReadFblockTree(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s/tree", srv.URL, hex.EncodeToString(uuid[:])))
	if err != nil {
		t.Fatalf("GET tree: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var root TreeNode
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if root.Role != mediatree.RoleRoot.String() {
		t.Fatalf("root.Role = %q, want %q", root.Role, mediatree.RoleRoot.String())
	}
	// Walk down to at least one node and confirm a bytes-typed node reports
	// Size, not Value, and never leaks Content payload bytes in the JSON.
	var found bool
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.Role == mediatree.RoleFrameDataVideo.String() {
			found = true
			if n.Size == nil || *n.Size != uint64(len("hello-frame")) {
				t.Errorf("frame_data(video).Size = %v, want %d", n.Size, len("hello-frame"))
			}
			if n.Value != "" {
				t.Errorf("frame_data(video).Value = %q, want empty (structure view only)", n.Value)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(&root)
	if !found {
		t.Fatal("expected a frame_data(video) node somewhere in the tree")
	}
}

func TestHandleReadFblockTree_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	srv := newTestServer(t, reg)
	resp, err := http.Get(srv.URL + "/storages/nope/fcontainers/" + unknownUUIDHex + "/tree")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleGetFblock(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
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
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
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
