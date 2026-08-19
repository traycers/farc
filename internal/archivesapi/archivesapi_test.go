package archivesapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(buf)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	err := json.NewDecoder(resp.Body).Decode(v)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// TestServer_FullFlow exercises every /api/v1/archives/* route end to end
// against a single archive through a real farcd underneath -- setup (with
// one channel), add a second channel, update channel config, start/stop
// recording, remove both channels, set ttl, then detach. Ported from the
// original in-process internal/api/archives_test.go's TestArchivesFullFlow,
// now one HTTP hop further out.
func TestServer_FullFlow(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	const aid = "arch1"
	dir := t.TempDir()

	setupReq := archiveConfigNew{
		ID: aid, TTL: 30, Path: filepath.Join(dir, "archive.img"), Size: 32, FSize: 8,
		Channels: []archiveChannelConfigNew{
			{Num: 1, URLs: []string{"rtsp://127.0.0.1:1/nonexistent"}},
		},
	}
	resp := doJSON(t, srv, http.MethodPut, "/api/v1/archives/", setupReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archives_setup status = %d, want 200", resp.StatusCode)
	}
	var setupResp struct {
		FblocksCount int64 `json:"fblocks_count"`
	}
	decodeBody(t, resp, &setupResp)
	if setupResp.FblocksCount != 4 {
		t.Fatalf("fblocks_count = %d, want 4 (32/8)", setupResp.FblocksCount)
	}

	addReq := []archiveChannelConfigNew{{Num: 2, PreRecord: 1, PostRecord: 2, URLs: []string{"rtsp://127.0.0.1:1/nonexistent"}}}
	resp = doJSON(t, srv, http.MethodPost, "/api/v1/archives/"+aid+"/channels/", addReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channels_add status = %d, want 200", resp.StatusCode)
	}

	cfgReq := []archiveChannelConfig{{Num: 1, PreRecord: 5, PostRecord: 6}}
	resp = doJSON(t, srv, http.MethodPatch, "/api/v1/archives/"+aid+"/channels/config/", cfgReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config_update status = %d, want 200", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodPost, "/api/v1/archives/"+aid+"/recording/start", archiveRecordingRequest{Channels: []uint16{1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording_start status = %d, want 200", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodPost, "/api/v1/archives/"+aid+"/recording/stop", archiveRecordingRequest{Channels: []uint16{1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording_stop status = %d, want 200", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodDelete, "/api/v1/archives/"+aid+"/channels/?id=1&id=2", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channels_del status = %d, want 200", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodPut, "/api/v1/archives/"+aid+"/ttl/", archiveTTLRequest{TTL: 10})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ttl_set status = %d, want 200", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodDelete, "/api/v1/archives/?id="+aid, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archives_detach status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleArchiveSetup_MissingRequiredFields(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodPut, "/api/v1/archives/", archiveConfigNew{ID: "a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body archiveErrorBody
	decodeBody(t, resp, &body)
	if body.Code != http.StatusBadRequest || body.Message == "" {
		t.Fatalf("body = %+v, want code=400 and a message", body)
	}
}

func TestHandleArchiveChannelsDel_UnknownArchiveIs404(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodDelete, "/api/v1/archives/nope/channels/?id=1", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body archiveErrorBody
	decodeBody(t, resp, &body)
	if body.Code != http.StatusNotFound {
		t.Fatalf("body.Code = %d, want 404", body.Code)
	}
}

func TestHandleArchiveChannelsConfigUpdate_UnknownArchiveIs404(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodPatch, "/api/v1/archives/nope/channels/config/", []archiveChannelConfig{{Num: 1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleArchiveRecording_UnknownArchiveIs404(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodPost, "/api/v1/archives/nope/recording/start", archiveRecordingRequest{Channels: []uint16{1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleArchiveDetach_UnknownArchiveIs404(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodDelete, "/api/v1/archives/?id=nope", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleArchiveTTLSet_UnknownArchiveIs404(t *testing.T) {
	farcd := newTestFarcd()
	defer farcd.Close()
	srv := newTestServer(farcd)
	defer srv.Close()

	resp := doJSON(t, srv, http.MethodPut, "/api/v1/archives/nope/ttl/", archiveTTLRequest{TTL: 5})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
