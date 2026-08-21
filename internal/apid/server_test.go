package apid_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/traycers/farc/internal/apid"
)

func newTestAPIServer(farcd apid.FarcdClient, mtx apid.MediamtxClient) *httptest.Server {
	orch := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)
	srv := apid.NewServer(orch)
	return httptest.NewServer(srv.Handler())
}

func doJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, out
}

func TestServer_CreateChannel_Success(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{}, &fakeMediamtxClient{})
	defer ts.Close()

	status, body := doJSON(t, ts, http.MethodPost, "/channels", map[string]any{
		"id":       1,
		"rtsp_url": "rtsp://camera/1",
		"storage":  "s1",
		"capture_policy": map[string]any{
			"type": "continuous",
		},
		"name": "front door",
	})

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}
	if body["farcd"] != "ok" || body["mediamtx"] != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestServer_CreateChannel_PartialFailure(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{createErr: errors.New("storage full")}, &fakeMediamtxClient{})
	defer ts.Close()

	status, body := doJSON(t, ts, http.MethodPost, "/channels", map[string]any{
		"id": 1, "rtsp_url": "rtsp://camera/1", "storage": "full",
		"capture_policy": map[string]any{"type": "continuous"},
	})

	if status != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", status)
	}
	farcdField, _ := body["farcd"].(string)
	if body["mediamtx"] != "ok" || farcdField == "ok" || farcdField == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestServer_CreateChannel_BadJSONRejected(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{}, &fakeMediamtxClient{})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/channels", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /channels: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_UpdateChannel_Success(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{channels: map[uint16]apid.ChannelInfo{1: {Channel: 1}}}, &fakeMediamtxClient{})
	defer ts.Close()

	status, body := doJSON(t, ts, http.MethodPatch, "/channels/1", map[string]any{
		"rtsp_url": "rtsp://camera/1-new", "storage": "s1",
		"capture_policy": map[string]any{"type": "continuous"},
		"name":           "renamed",
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["farcd"] != "ok" || body["mediamtx"] != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestServer_RemoveChannel_Success(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{channels: map[uint16]apid.ChannelInfo{1: {Channel: 1}}}, &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/1"}})
	defer ts.Close()

	status, body := doJSON(t, ts, http.MethodDelete, "/channels/1", nil)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["farcd"] != "ok" || body["mediamtx"] != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestServer_GetChannel_CameraURL(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{}, &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/1"}})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/channels/1")
	if err != nil {
		t.Fatalf("GET /channels/1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		CameraRTSPURL string `json:"camera_rtsp_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.CameraRTSPURL != "rtsp://camera/1" {
		t.Fatalf("camera_rtsp_url = %q", out.CameraRTSPURL)
	}
}

func TestServer_GetChannel_NotFound(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{}, &fakeMediamtxClient{})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/channels/1")
	if err != nil {
		t.Fatalf("GET /channels/1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestServer_GetLiveURLs(t *testing.T) {
	ts := newTestAPIServer(&fakeFarcdClient{}, &fakeMediamtxClient{})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/channels/live-urls?ids=1,2")
	if err != nil {
		t.Fatalf("GET live-urls: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		URLs map[string]string `json:"urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.URLs["1"] != "http://mediamtx:8889/1/whep" || out.URLs["2"] != "http://mediamtx:8889/2/whep" {
		t.Fatalf("urls = %+v", out.URLs)
	}
}
