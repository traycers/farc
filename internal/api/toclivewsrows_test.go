package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/storage"
)

func dialFblockLiveTOCRows(t *testing.T, srv *httptest.Server, storageID string, idx uint32) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/storages/" + storageID + "/fblocks/" + strconv.FormatUint(uint64(idx), 10) + "/toc/rows/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readFblockLiveTOCRowsMsg(t *testing.T, conn *websocket.Conn) tocRowsLiveMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var msg tocRowsLiveMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read fblock live-toc-rows message: %v", err)
	}
	return msg
}

func TestHandleFblockLiveTOCRowsWS_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/storages/nope/fblocks/0/toc/rows/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleFblockLiveTOCRowsWS_NoIngestManager(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg) // ing == nil
	resp, err := http.Get(srv.URL + "/storages/s1/fblocks/0/toc/rows/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestHandleFblockLiveTOCRowsWS_IndexOutOfRange(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks/999999/toc/rows/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleFblockLiveTOCRowsWS_NotInProgress(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t) // freshly initialized: fblock 0 is "uninitialized", not in_progress
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/storages/s1/fblocks/0/toc/rows/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestHandleFblockLiveTOCRowsWS_ConnectsAndSendsEmptyRowsWhenNoIngestData
// mirrors TestHandleFblockLiveTreeWS_ConnectsAndSendsEmptyTreeWhenNoIngestData:
// im has no channel configured for "s1", so LiveTreeForStorage("s1")
// reports ok=false -- the handler must still connect and send a
// (rows=nil) message rather than erroring.
func TestHandleFblockLiveTOCRowsWS_ConnectsAndSendsEmptyRowsWhenNoIngestData(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if _, _, _, err := u.BeginSegment([]uint16{1}, 100); err != nil {
		t.Fatalf("BeginSegment: %v", err)
	}
	if err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	snap := u.Index().Snapshot()
	var idx uint32
	var found bool
	for i := uint32(0); i < snap.N; i++ {
		if snap.State(i) == fblock.InProgress {
			idx, found = i, true
			break
		}
	}
	if !found {
		t.Fatalf("BeginSegment: no in_progress fblock found in snapshot %+v", snap)
	}

	conn := dialFblockLiveTOCRows(t, srv, "s1", idx)
	msg := readFblockLiveTOCRowsMsg(t, conn)
	if msg.Rows != nil {
		t.Fatalf("Rows = %+v, want nil (no ingest data observed for this storage)", msg.Rows)
	}
}
