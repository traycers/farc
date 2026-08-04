package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/ingest"
)

type fakeRecorder struct{}

func (fakeRecorder) WriteFcontainer(channels []uint16, begin, end uint64, filler *fcontainer.Filler, now uint64) ([16]byte, error) {
	return [16]byte{}, nil
}

// newTestIngestManager starts one channel against an RTSP URL nothing is
// listening on -- ChannelIngest.Run fails fast (connection refused) and its
// goroutine exits, but the CapturePolicy itself (created synchronously in
// startLocked, before the goroutine even runs) stays reachable via
// IngestManager's channel map, which is all SetPolicy/TriggerEvent touch.
func newTestIngestManager(t *testing.T) *ingest.IngestManager {
	t.Helper()
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{{
		Channel:      1,
		RTSPURL:      "rtsp://127.0.0.1:1/nonexistent",
		Recorder:     fakeRecorder{},
		QueueDepth:   uint64(time.Second),
		PolicyType:   ingest.PolicyContinuous,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	}})
	t.Cleanup(im.Stop)
	return im
}

func TestHandleSetCapturePolicy(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "event"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHandleSetCapturePolicy_ScheduleNotImplemented(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "schedule"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestHandleSetCapturePolicy_UnknownChannel(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/999/capture-policy", setCapturePolicyRequest{Type: "continuous"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleSetCapturePolicy_NoIngestManager(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "continuous"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestHandleTriggerEvent(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Trigger is only valid once the channel is on the event policy.
	resp1 := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "event"})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusNoContent {
		t.Fatalf("capture-policy status = %d, want 204", resp1.StatusCode)
	}

	resp2 := postJSON(t, srv, "/channels/1/events", triggerEventRequest{})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("events status = %d, want 204", resp2.StatusCode)
	}
}

func TestHandleTriggerEvent_WrongPolicyType(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Channel 1 starts on PolicyContinuous (newTestIngestManager) -- Trigger
	// is event-only.
	resp := postJSON(t, srv, "/channels/1/events", triggerEventRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleTriggerEvent_UnknownChannel(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/999/events", triggerEventRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
