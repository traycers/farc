package api

import (
	"fmt"
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

func TestHandleStartRecording(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/recording/start", startRecordingRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHandleStartRecording_WithFromTime(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	from := uint64(123)
	resp := postJSON(t, srv, "/channels/1/recording/start", startRecordingRequest{FromTimeNS: &from})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHandleStartRecording_WrongPolicyType(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp1 := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "event"})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusNoContent {
		t.Fatalf("capture-policy status = %d, want 204", resp1.StatusCode)
	}

	resp := postJSON(t, srv, "/channels/1/recording/start", startRecordingRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleStartRecording_UnknownChannel(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/999/recording/start", startRecordingRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleStartRecording_NoIngestManager(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/recording/start", startRecordingRequest{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestHandleStopRecording(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/recording/stop", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestHandleStopRecording_WrongPolicyType(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp1 := postJSON(t, srv, "/channels/1/capture-policy", setCapturePolicyRequest{Type: "event"})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusNoContent {
		t.Fatalf("capture-policy status = %d, want 204", resp1.StatusCode)
	}

	resp := postJSON(t, srv, "/channels/1/recording/stop", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleStopRecording_UnknownChannel(t *testing.T) {
	im := newTestIngestManager(t)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/999/recording/stop", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleStopRecording_NoIngestManager(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels/1/recording/stop", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func regTestUnit(t *testing.T, reg *StorageRegistry, id string) {
	t.Helper()
	if err := reg.Register(id, newTestUnit(t), id+".img", ""); err != nil {
		t.Fatalf("Register(%q): %v", id, err)
	}
}

func TestHandleCreateChannel_StartsItAndListsIt(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createChannelRequest{
		ID: 5, RTSPURL: "rtsp://127.0.0.1:1/x", Storage: "s1",
		CapturePolicy: channelCapturePolicyRequest{Type: "continuous", MaxDeferredStartNS: 30_000_000_000},
	}
	resp := postJSON(t, srv, "/channels", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/channels")
	if err != nil {
		t.Fatalf("GET /channels: %v", err)
	}
	defer getResp.Body.Close()
	var list []channelInfo
	if err := decodeBody(getResp, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Channel != 5 || list[0].RTSPURL != req.RTSPURL || list[0].Storage != "s1" || list[0].PolicyType != "continuous" {
		t.Fatalf("list = %+v", list)
	}
}

func TestHandleCreateChannel_ChannelZeroRejected(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 0, RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateChannel_UnknownStorageRejected(t *testing.T) {
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://x/y", Storage: "nope", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleCreateChannel_DuplicateIDConflicts(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createChannelRequest{ID: 1, RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"}}
	resp1 := postJSON(t, srv, "/channels", req)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", resp1.StatusCode)
	}

	resp2 := postJSON(t, srv, "/channels", req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", resp2.StatusCode)
	}
}

func TestHandleCreateChannel_OnChannelCreatedErrorRemovesChannel(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	s.SetOnChannelCreated(func(ChannelSpec) error { return fmt.Errorf("disk full") })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if len(im.List()) != 0 {
		t.Fatalf("channel should have been removed after a failed persist, got %+v", im.List())
	}
}

func TestHandleUpdateChannel_ReplacesFieldsAndPersists(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	regTestUnit(t, reg, "s2")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	create := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://old/x", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	create.Body.Close()
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", create.StatusCode)
	}

	update := putJSON(t, srv, "/channels/1", updateChannelRequest{
		RTSPURL: "rtsp://new/y", Storage: "s2", CapturePolicy: channelCapturePolicyRequest{Type: "event", PrerecordNS: 5, PostrecordNS: 10},
	})
	defer update.Body.Close()
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", update.StatusCode)
	}

	list := im.List()
	if len(list) != 1 || list[0].RTSPURL != "rtsp://new/y" || list[0].StorageID != "s2" || list[0].PolicyType != ingest.PolicyEvent {
		t.Fatalf("list after update = %+v", list)
	}
}

func TestHandleUpdateChannel_UnknownChannelIs404(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := putJSON(t, srv, "/channels/999", updateChannelRequest{
		RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleRemoveChannel_StopsItAndDelistsIt(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnit(t, reg, "s1")
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	create := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	create.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/channels/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if len(im.List()) != 0 {
		t.Fatalf("channel still listed after delete: %+v", im.List())
	}
}

func TestHandleRemoveChannel_UnknownChannelIs404(t *testing.T) {
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/channels/999", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
