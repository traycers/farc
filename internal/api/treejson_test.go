package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

func getTreeLevel(t *testing.T, srv *httptest.Server, storageID, uuidHex string, node *uint32) treeLevelResponse {
	t.Helper()
	url := fmt.Sprintf("%s/storages/%s/fcontainers/%s/tree", srv.URL, storageID, uuidHex)
	if node != nil {
		url += fmt.Sprintf("?node=%d", *node)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET tree: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out treeLevelResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func findChild(children []treeNodeJSON, role string) (treeNodeJSON, bool) {
	for _, c := range children {
		if c.Role == role {
			return c, true
		}
	}
	return treeNodeJSON{}, false
}

// descend walks from the root through path (a sequence of Role names,
// root's children's role, then that node's children's role, and so on),
// GETting one level at a time — the same navigation a status-page client
// does, minus the UI.
func descend(t *testing.T, srv *httptest.Server, storageID, uuidHex string, path ...string) treeNodeJSON {
	t.Helper()
	lvl := getTreeLevel(t, srv, storageID, uuidHex, nil)
	node := lvl.Node
	for _, role := range path {
		child, ok := findChild(lvl.Children, role)
		if !ok {
			t.Fatalf("no %q child of node %d (%s)", role, node.ID, node.Role)
		}
		node = child
		lvl = getTreeLevel(t, srv, storageID, uuidHex, &node.ID)
	}
	return node
}

func TestHandleReadTree_WalksVideoFrame(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "hello-frame", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)
	uuidHex := hex.EncodeToString(uuid[:])

	root := getTreeLevel(t, srv, "s1", uuidHex, nil)
	if root.Node.Role != "root" || root.Total != 1 {
		t.Fatalf("root = %+v", root)
	}

	channel := descend(t, srv, "s1", uuidHex, "channels", "channel")
	if v, ok := channel.Value.(float64); !ok || v != 1 {
		t.Fatalf("channel.Value = %#v, want 1", channel.Value)
	}

	config := descend(t, srv, "s1", uuidHex, "channels", "channel", "streams", "stream", "video", "configs(video)", "config(video)")
	lvl := getTreeLevel(t, srv, "s1", uuidHex, &config.ID)
	data, ok := findChild(lvl.Children, "data(video)")
	if !ok {
		t.Fatal("no data(video) child")
	}
	frames, ok := findChild(lvl.Children, "frames(video)")
	if !ok {
		t.Fatal("no frames(video) child")
	}
	if frames.ChildCount != 1 {
		t.Fatalf("frames(video).ChildCount = %d, want 1", frames.ChildCount)
	}

	lvl = getTreeLevel(t, srv, "s1", uuidHex, &data.ID)
	sps, ok := findChild(lvl.Children, "param_sps")
	if !ok {
		t.Fatal("no param_sps child")
	}
	if sps.Size != 3 || sps.Value != nil {
		t.Fatalf("param_sps = %+v, want size=3, no value", sps)
	}

	lvl = getTreeLevel(t, srv, "s1", uuidHex, &frames.ID)
	frame, ok := findChild(lvl.Children, "frame(video)")
	if !ok {
		t.Fatal("no frame(video) child")
	}
	lvl = getTreeLevel(t, srv, "s1", uuidHex, &frame.ID)
	frameData, ok := findChild(lvl.Children, "frame_data(video)")
	if !ok {
		t.Fatal("no frame_data(video) child")
	}
	if frameData.Size != uint64(len("hello-frame")) || frameData.Value != nil {
		t.Fatalf("frame_data = %+v, want size=%d, no value", frameData, len("hello-frame"))
	}
	frameTime, ok := findChild(lvl.Children, "frame_time(video)")
	if !ok {
		t.Fatal("no frame_time(video) child")
	}
	if frameTime.Value != "100" {
		t.Fatalf("frame_time.Value = %#v, want \"100\" (string, ns timestamps can exceed JS's safe-integer range)", frameTime.Value)
	}
}

func TestHandleReadTree_UnknownStorage(t *testing.T) {
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

func TestHandleReadTree_NodeOutOfRange(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 100, "x", 100, 1000)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)
	resp, err := http.Get(fmt.Sprintf("%s/storages/s1/fcontainers/%s/tree?node=999999", srv.URL, hex.EncodeToString(uuid[:])))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleReadTree_Pagination(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	f := fcontainer.New()
	configID, err := f.AddStreamParams(1, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time: 0, CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1}, ParamPPS: []byte{2},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	frames := make([]fcontainer.Frame, 5)
	for i := range frames {
		frames[i] = fcontainer.Frame{Data: []byte{byte(i)}, Time: uint64(i), Kind: mediatree.FrameKindI}
	}
	if err := f.AddFrames(configID, frames); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	uuid, err := u.WriteFcontainer([]uint16{1}, 0, 4, f, 1000)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)
	uuidHex := hex.EncodeToString(uuid[:])

	framesNode := descend(t, srv, "s1", uuidHex, "channels", "channel", "streams", "stream", "video", "configs(video)", "config(video)", "frames(video)")
	if framesNode.ChildCount != 5 {
		t.Fatalf("frames(video).ChildCount = %d, want 5", framesNode.ChildCount)
	}

	seen := make(map[uint32]bool)
	for _, page := range []struct{ offset, limit, wantLen int }{
		{0, 2, 2}, {2, 2, 2}, {4, 2, 1}, {6, 2, 0},
	} {
		url := fmt.Sprintf("%s/storages/s1/fcontainers/%s/tree?node=%d&offset=%d&limit=%d",
			srv.URL, uuidHex, framesNode.ID, page.offset, page.limit)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		var lvl treeLevelResponse
		err = json.NewDecoder(resp.Body).Decode(&lvl)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if lvl.Total != 5 {
			t.Fatalf("offset=%d limit=%d: Total = %d, want 5", page.offset, page.limit, lvl.Total)
		}
		if len(lvl.Children) != page.wantLen {
			t.Fatalf("offset=%d limit=%d: got %d children, want %d", page.offset, page.limit, len(lvl.Children), page.wantLen)
		}
		for _, c := range lvl.Children {
			if seen[c.ID] {
				t.Fatalf("frame id %d returned by more than one page", c.ID)
			}
			seen[c.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("total distinct frame ids seen across pages = %d, want 5", len(seen))
	}
}
