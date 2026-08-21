package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// TestTocRowJSONEncodesValueOrOffsetAsString guards against silent
// precision loss: a timestamp-typed node's packed inline value is a unix-ns
// uint64 (~1.7e18), well past JS's 2^53 safe-integer limit -- a bare JSON
// number would round in the browser (the same reason TreeNode.Value is
// already a decimal string, not a number). A Go-side json.Marshal/Unmarshal
// round trip alone would NOT catch this (float64-shaped loss only shows up
// parsing the raw wire bytes as a browser's JSON.parse would), so this
// asserts against the literal encoded bytes.
func TestTocRowJSONEncodesValueOrOffsetAsString(t *testing.T) {
	row := tocRow{ID: 0, Type: "timestamp", Role: "frame_time(video)", ValueOrOffset: 1700000000123456789}
	buf, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"value_or_offset":"1700000000123456789"`) {
		t.Fatalf("json = %s, want a quoted string value_or_offset (unquoted risks JS float64 precision loss)", buf)
	}
}

func TestTocRowsFromColumns(t *testing.T) {
	elems := []mediatree.Element{
		{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Parent: 0, Sibling: 1, Value: u32le(5)},
		{Type: mediatree.TypeBytes, Role: mediatree.RoleFrameDataVideo, Parent: 0, Sibling: 1, Value: []byte("abc")},
	}
	offsets := []uint64{0, 0, 42}
	cols, err := toc.Build(elems, offsets)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rows := tocRowsFromColumns(cols)
	want := []tocRow{
		{ID: 0, Type: "void", Role: mediatree.RoleRoot.String(), ParentID: 0, SiblingID: 0, ValueOrOffset: 0, Size: 0},
		{ID: 1, Type: "uint32", Role: mediatree.RoleChannel.String(), ParentID: 0, SiblingID: 1, ValueOrOffset: 5, Size: 0},
		{ID: 2, Type: "bytes", Role: mediatree.RoleFrameDataVideo.String(), ParentID: 0, SiblingID: 1, ValueOrOffset: 42, Size: 3},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

func TestLiveValueOrOffset(t *testing.T) {
	cases := []struct {
		name string
		e    mediatree.Element
		want uint64
	}{
		{"fixed-width uint32 packs inline like toc.Build", mediatree.Element{Type: mediatree.TypeUint32, Value: u32le(5)}, 5},
		{"variable-width bytes has no live Content offset", mediatree.Element{Type: mediatree.TypeBytes, Value: []byte("abc")}, 0},
		{"variable-width string has no live Content offset", mediatree.Element{Type: mediatree.TypeString, Value: []byte("hi")}, 0},
	}
	for _, c := range cases {
		if got := liveValueOrOffset(c.e); got != c.want {
			t.Errorf("%s: liveValueOrOffset(%+v) = %d, want %d", c.name, c.e, got, c.want)
		}
	}
}

func TestLiveSize(t *testing.T) {
	cases := []struct {
		name string
		e    mediatree.Element
		want uint64
	}{
		{"fixed-width uint32 reports 0, matching toc.Columns.Size for fixed types", mediatree.Element{Type: mediatree.TypeUint32, Value: u32le(5)}, 0},
		{"variable-width bytes reports its byte length", mediatree.Element{Type: mediatree.TypeBytes, Value: []byte("abc")}, 3},
	}
	for _, c := range cases {
		if got := liveSize(c.e); got != c.want {
			t.Errorf("%s: liveSize(%+v) = %d, want %d", c.name, c.e, got, c.want)
		}
	}
}

func TestTocRowsFromElements(t *testing.T) {
	elems := []mediatree.Element{
		{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Parent: 0, Sibling: 1, Value: u32le(5)},
		{Type: mediatree.TypeBytes, Role: mediatree.RoleFrameDataVideo, Parent: 0, Sibling: 1, Value: []byte("abc")},
	}
	rows := tocRowsFromElements(elems)
	want := []tocRow{
		{ID: 0, Type: "void", Role: mediatree.RoleRoot.String(), ParentID: 0, SiblingID: 0, ValueOrOffset: 0, Size: 0},
		{ID: 1, Type: "uint32", Role: mediatree.RoleChannel.String(), ParentID: 0, SiblingID: 1, ValueOrOffset: 5, Size: 0},
		{ID: 2, Type: "bytes", Role: mediatree.RoleFrameDataVideo.String(), ParentID: 0, SiblingID: 1, ValueOrOffset: 0, Size: 3},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
}

func TestHandleReadTOCRows(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s/toc/rows", srv.URL, hex.EncodeToString(uuid[:])))
	if err != nil {
		t.Fatalf("GET toc/rows: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []tocRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Cross-check row count directly against the real *toc.Columns for this
	// fblock (the actual source of truth this handler reads) -- rather than
	// a second HTTP round trip to a tree endpoint, which no longer exists
	// (issue 04 removed /tree in favor of the client building its own tree
	// from these rows).
	columns, err := u.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	if len(rows) != int(columns.N) {
		t.Fatalf("len(rows) = %d, want %d (toc.Columns.N)", len(rows), columns.N)
	}

	var found bool
	for _, row := range rows {
		if row.Role == mediatree.RoleFrameDataVideo.String() {
			found = true
			if row.Size != uint64(len("hello-frame")) {
				t.Errorf("frame_data(video).Size = %d, want %d", row.Size, len("hello-frame"))
			}
		}
	}
	if !found {
		t.Fatal("expected a frame_data(video) row somewhere in the result")
	}
}

func TestHandleReadTOCRows_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	srv := newTestServer(t, reg)
	resp, err := http.Get(srv.URL + "/storages/nope/fcontainers/" + unknownUUIDHex + "/toc/rows")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
