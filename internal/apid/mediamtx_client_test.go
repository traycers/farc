package apid_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/traycers/farc/internal/apid"
)

// newFakeMediamtxServer is a hand-rolled fake -- no real mediamtx binary is
// embedded in this repo (unlike farcd, see farcd_client_test.go) -- exposed
// requests are recorded into got for assertions, and the response for each
// path is looked up in responses (defaulting to {"status":"ok"}, HTTP 200,
// if the path isn't in responses).
type fakeMediamtxRequest struct {
	method string
	path   string
	body   map[string]any
}

func newFakeMediamtxServer(t *testing.T, responses map[string]int) (*httptest.Server, *[]fakeMediamtxRequest) {
	t.Helper()
	var got []fakeMediamtxRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		buf, _ := io.ReadAll(r.Body)
		if len(buf) > 0 {
			_ = json.Unmarshal(buf, &body)
		}
		got = append(got, fakeMediamtxRequest{method: r.Method, path: r.URL.Path, body: body})

		status, ok := responses[r.Method+" "+r.URL.Path]
		if !ok {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status < 300 {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom", "status": "error"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestMediamtxClient_AddPath(t *testing.T) {
	srv, got := newFakeMediamtxServer(t, nil)
	client := apid.NewMediamtxClient(srv.URL)

	err := client.AddPath(context.Background(), "1", "rtsp://camera/1")
	if err != nil {
		t.Fatalf("AddPath: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("requests = %+v", *got)
	}
	r := (*got)[0]
	if r.method != http.MethodPost || r.path != "/v3/config/paths/add/1" {
		t.Fatalf("request = %+v", r)
	}
	if r.body["source"] != "rtsp://camera/1" {
		t.Fatalf("body = %+v", r.body)
	}
}

func TestMediamtxClient_PatchPath(t *testing.T) {
	srv, got := newFakeMediamtxServer(t, nil)
	client := apid.NewMediamtxClient(srv.URL)

	err := client.PatchPath(context.Background(), "1", "rtsp://camera/1-new")
	if err != nil {
		t.Fatalf("PatchPath: %v", err)
	}
	r := (*got)[0]
	if r.method != http.MethodPatch || r.path != "/v3/config/paths/patch/1" {
		t.Fatalf("request = %+v", r)
	}
	if r.body["source"] != "rtsp://camera/1-new" {
		t.Fatalf("body = %+v", r.body)
	}
}

func TestMediamtxClient_DeletePath(t *testing.T) {
	srv, got := newFakeMediamtxServer(t, nil)
	client := apid.NewMediamtxClient(srv.URL)

	err := client.DeletePath(context.Background(), "1")
	if err != nil {
		t.Fatalf("DeletePath: %v", err)
	}
	r := (*got)[0]
	if r.method != http.MethodDelete || r.path != "/v3/config/paths/delete/1" {
		t.Fatalf("request = %+v", r)
	}
}

func TestMediamtxClient_DeletePath_AlreadyGoneIsNotAnError(t *testing.T) {
	srv, _ := newFakeMediamtxServer(t, map[string]int{
		"DELETE /v3/config/paths/delete/1": http.StatusNotFound,
	})
	client := apid.NewMediamtxClient(srv.URL)

	// Deleting an already-absent path must be idempotent
	// (.scratch/live-page/issues/01-apid-server.md's no-rollback/
	// idempotent-retry design), not an error.
	err := client.DeletePath(context.Background(), "1")
	if err != nil {
		t.Fatalf("DeletePath of an absent path: %v, want nil (idempotent)", err)
	}
}

func TestMediamtxClient_PathExists(t *testing.T) {
	srv, got := newFakeMediamtxServer(t, map[string]int{
		"GET /v3/config/paths/get/1": http.StatusOK,
		"GET /v3/config/paths/get/2": http.StatusNotFound,
	})
	client := apid.NewMediamtxClient(srv.URL)

	exists, err := client.PathExists(context.Background(), "1")
	if err != nil || !exists {
		t.Fatalf("PathExists(1) = %v, %v, want true, nil", exists, err)
	}
	exists, err = client.PathExists(context.Background(), "2")
	if err != nil || exists {
		t.Fatalf("PathExists(2) = %v, %v, want false, nil", exists, err)
	}
	if len(*got) != 2 {
		t.Fatalf("requests = %+v", *got)
	}
}

func newFakeMediamtxServerWithSource(t *testing.T, name, source string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config/paths/get/"+name {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found", "status": "error"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"source": source})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMediamtxClient_GetPathSource(t *testing.T) {
	srv := newFakeMediamtxServerWithSource(t, "1", "rtsp://camera/1")
	client := apid.NewMediamtxClient(srv.URL)

	source, exists, err := client.GetPathSource(context.Background(), "1")
	if err != nil || !exists || source != "rtsp://camera/1" {
		t.Fatalf("GetPathSource(1) = %q, %v, %v, want rtsp://camera/1, true, nil", source, exists, err)
	}
}

func TestMediamtxClient_GetPathSource_NotFound(t *testing.T) {
	srv := newFakeMediamtxServerWithSource(t, "1", "rtsp://camera/1")
	client := apid.NewMediamtxClient(srv.URL)

	source, exists, err := client.GetPathSource(context.Background(), "2")
	if err != nil || exists || source != "" {
		t.Fatalf("GetPathSource(2) = %q, %v, %v, want \"\", false, nil", source, exists, err)
	}
}

func TestMediamtxClient_AddPath_ErrorSurfacesMediamtxMessage(t *testing.T) {
	srv, _ := newFakeMediamtxServer(t, map[string]int{
		"POST /v3/config/paths/add/1": http.StatusBadRequest,
	})
	client := apid.NewMediamtxClient(srv.URL)

	err := client.AddPath(context.Background(), "1", "rtsp://camera/1")
	if err == nil {
		t.Fatalf("AddPath: want error, got nil")
	}
}
