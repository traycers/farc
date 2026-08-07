package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"traycers/farc/internal/ingest"
	"traycers/farc/internal/ioengine"
)

// requireDirectBackend skips the test if O_DIRECT isn't usable under dir
// (e.g. tmpfs -- common for t.TempDir() in CI/sandboxes), mirroring
// internal/ioengine's own openDirectOrSkip: archives_setup never lets a
// caller pick "standard" (models.archive.ConfigNew has no backend field), so
// this exercises the same default path production traffic gets.
func requireDirectBackend(t *testing.T, dir string) {
	t.Helper()
	b, err := ioengine.Open(filepath.Join(dir, "probe.img"), ioengine.Options{})
	if err != nil {
		t.Skipf("O_DIRECT not usable on this filesystem (%s): %v", dir, err)
	}
	_ = b.Close()
	_ = os.Remove(filepath.Join(dir, "probe.img"))
}

func methodJSON(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
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

// TestArchivesFullFlow exercises every /api/v1/archives/* route end to end
// against a single archive: setup (with one channel), add a second channel,
// update channel config, start/stop recording, remove both channels, set
// ttl, then detach -- the sequence msm is expected to drive farcd through.
func TestArchivesFullFlow(t *testing.T) {
	dir := t.TempDir()
	requireDirectBackend(t, dir)

	im := ingest.NewIngestManager()
	im.Start(nil)
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	const aid = "arch1"

	setupReq := archiveConfigNew{
		ID:    aid,
		TTL:   30,
		Path:  filepath.Join(dir, "archive.img"),
		Size:  32,
		FSize: 8,
		Channels: []archiveChannelConfigNew{
			{Num: 1, URLs: []string{"rtsp://127.0.0.1:1/nonexistent"}},
		},
	}
	resp := putJSON(t, srv, "/api/v1/archives/", setupReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archives_setup status = %d, want 200", resp.StatusCode)
	}
	var setupResp struct {
		FblocksCount int64 `json:"fblocks_count"`
	}
	if err := decodeBody(resp, &setupResp); err != nil {
		t.Fatalf("decode archives_setup response: %v", err)
	}
	if setupResp.FblocksCount != 4 {
		t.Fatalf("fblocks_count = %d, want 4 (32/8)", setupResp.FblocksCount)
	}
	if _, ok := s.reg.Get(aid); !ok {
		t.Fatal("archive not registered as a Storage after archives_setup")
	}
	if _, ok := s.findChannel(1); !ok {
		t.Fatal("channel 1 not running after archives_setup")
	}

	addReq := []archiveChannelConfigNew{{Num: 2, PreRecord: 1, PostRecord: 2, URLs: []string{"rtsp://127.0.0.1:1/nonexistent"}}}
	resp = postJSON(t, srv, "/api/v1/archives/"+aid+"/channels/", addReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channels_add status = %d, want 200", resp.StatusCode)
	}
	if _, ok := s.findChannel(2); !ok {
		t.Fatal("channel 2 not running after channels_add")
	}

	cfgReq := []archiveChannelConfig{{Num: 1, PreRecord: 5, PostRecord: 6}}
	resp = methodJSON(t, srv, http.MethodPatch, "/api/v1/archives/"+aid+"/channels/config/", cfgReq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config_update status = %d, want 200", resp.StatusCode)
	}
	info, ok := s.findChannel(1)
	if !ok || info.PolicyParams.Prerecord != secondsToNS(5) || info.PolicyParams.Postrecord != secondsToNS(6) {
		t.Fatalf("channel 1 policy params after config_update = %+v", info.PolicyParams)
	}

	resp = postJSON(t, srv, "/api/v1/archives/"+aid+"/recording/start", archiveRecordingRequest{Channels: []uint16{1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording_start status = %d, want 200", resp.StatusCode)
	}

	resp = postJSON(t, srv, "/api/v1/archives/"+aid+"/recording/stop", archiveRecordingRequest{Channels: []uint16{1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording_stop status = %d, want 200", resp.StatusCode)
	}

	resp = methodJSON(t, srv, http.MethodDelete, "/api/v1/archives/"+aid+"/channels/?id=1&id=2", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channels_del status = %d, want 200", resp.StatusCode)
	}
	if _, ok := s.findChannel(1); ok {
		t.Fatal("channel 1 still running after channels_del")
	}
	if _, ok := s.findChannel(2); ok {
		t.Fatal("channel 2 still running after channels_del")
	}

	resp = putJSON(t, srv, "/api/v1/archives/"+aid+"/ttl/", archiveTTLRequest{TTL: 10})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ttl_set status = %d, want 200", resp.StatusCode)
	}

	resp = methodJSON(t, srv, http.MethodDelete, "/api/v1/archives/?id="+aid, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archives_detach status = %d, want 200", resp.StatusCode)
	}
	if _, ok := s.reg.Get(aid); ok {
		t.Fatal("archive still registered after archives_detach")
	}
}

func TestArchiveSetup_MissingRequiredFields(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := putJSON(t, srv, "/api/v1/archives/", archiveConfigNew{ID: "a"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body archiveErrorBody
	if err := decodeBody(resp, &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Message == "" {
		t.Fatal("expected a non-empty message in the {code,message} error body")
	}
}

func TestArchiveDetach_UnknownArchive(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := methodJSON(t, srv, http.MethodDelete, "/api/v1/archives/?id=nope", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
