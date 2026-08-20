package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
)

// fakeSegmentBackend/fakeAPISegment are minimal ingest.SegmentBackend/
// storage.Segment fakes for tests that only exercise admin-command
// dispatch (SetPolicy/TriggerEvent/List/StartRecording's own bookkeeping)
// -- none of these tests ever push a real frame through HandleFrame, so
// BeginSegment is never actually invoked; these exist purely to satisfy
// the interface.
type fakeSegmentBackend struct{}

func (fakeSegmentBackend) BeginSegment(channels []uint16, now uint64) (storage.Segment, storage.PoolStatus, int64, error) {
	return fakeAPISegment{}, storage.PoolNormal, 0, nil
}

type fakeAPISegment struct{}

func (fakeAPISegment) AddStreamParams(channel, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (uint32, error) {
	return 0, nil
}
func (fakeAPISegment) AddFrames(configID uint32, frames []fcontainer.Frame) error { return nil }
func (fakeAPISegment) RegisterChannel(channel uint16) error                       { return nil }
func (fakeAPISegment) Elements() []mediatree.Element                              { return nil }
func (fakeAPISegment) Close(now uint64) ([16]byte, error)                         { return [16]byte{}, nil }

// newTestIngestManager starts one channel against an RTSP URL nothing is
// listening on -- ChannelIngest.Run fails fast (connection refused) and its
// goroutine exits, but the CapturePolicy itself (created synchronously in
// startLocked, before the goroutine even runs) stays reachable via
// IngestManager's channel map, which is all SetPolicy/TriggerEvent touch.
func newTestIngestManager(t *testing.T) *ingest.IngestManager {
	t.Helper()
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{{
		Channel:        1,
		RTSPURL:        "rtsp://127.0.0.1:1/nonexistent",
		SegmentBackend: fakeSegmentBackend{},
		QueueDepth:     uint64(time.Second),
		PolicyType:     ingest.PolicyContinuous,
		ReadTimeout:    time.Second,
		WriteTimeout:   time.Second,
	}})
	t.Cleanup(im.Stop)
	return im
}

func TestHandleListChannels_FiltersByStorage(t *testing.T) {
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{
		{
			Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "a",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
		{
			Channel: 2, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "b",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
	})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/channels?storage=a")
	if err != nil {
		t.Fatalf("GET /channels?storage=a: %v", err)
	}
	defer resp.Body.Close()
	var list []channelInfo
	err = decodeBody(resp, &list)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Channel != 1 || list[0].Storage != "a" {
		t.Fatalf("list = %+v, want exactly channel 1 (storage a)", list)
	}
}

func TestHandleListChannels_NoStorageFilterReturnsAll(t *testing.T) {
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{
		{
			Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "a",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
		{
			Channel: 2, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "b",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
	})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/channels")
	if err != nil {
		t.Fatalf("GET /channels: %v", err)
	}
	defer resp.Body.Close()
	var list []channelInfo
	err = decodeBody(resp, &list)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2 channels", list)
	}
}

func TestHandleListChannels_ReflectsRecordingState(t *testing.T) {
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{
		{
			Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "a",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
	})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	get := func() []channelInfo {
		resp, err := http.Get(srv.URL + "/channels")
		if err != nil {
			t.Fatalf("GET /channels: %v", err)
		}
		defer resp.Body.Close()
		var list []channelInfo
		if err := decodeBody(resp, &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return list
	}

	if list := get(); len(list) != 1 || list[0].Recording {
		t.Fatalf("list before StartRecording = %+v, want Recording=false", list)
	}

	if err := im.StartRecording(1, 0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if list := get(); len(list) != 1 || !list[0].Recording {
		t.Fatalf("list after StartRecording = %+v, want Recording=true", list)
	}
}

// TestHandleListChannels_ReflectsLastConnectError relies on the real (fast)
// connection-refused failure of an unreachable rtsp_url, same convention as
// internal/ingest's own real-network tests, to exercise
// ChannelIngest.LastConnectError end to end through GET /channels.
func TestHandleListChannels_ReflectsLastConnectError(t *testing.T) {
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{
		{
			Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "a",
			SegmentBackend: fakeSegmentBackend{}, QueueDepth: uint64(time.Second),
			PolicyType: ingest.PolicyContinuous, ReadTimeout: time.Second, WriteTimeout: time.Second,
		},
	})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(NewStorageRegistry(), im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	deadline := time.After(2 * time.Second)
	for {
		resp, err := http.Get(srv.URL + "/channels")
		if err != nil {
			t.Fatalf("GET /channels: %v", err)
		}
		var list []channelInfo
		err = decodeBody(resp, &list)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(list) == 1 && list[0].LastConnectError != "" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("GET /channels' last_connect_error never became non-empty for an unreachable rtsp_url")
		case <-time.After(5 * time.Millisecond):
		}
	}
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
	err := reg.Register(id, newTestUnit(t), id+".img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register(%q): %v", id, err)
	}
}

func regTestUnitWithGeometry(t *testing.T, reg *StorageRegistry, id string, geo storage.Geometry) {
	t.Helper()
	err := reg.Register(id, newTestUnitWithGeometry(t, geo), id+".img", "", storage.PoolTuning{})
	if err != nil {
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
	err = decodeBody(getResp, &list)
	if err != nil {
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
	s.SetOnChannelCreated(func(ChannelSpec) error { return errors.New("disk full") })
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

func oneChannelGeometry() storage.Geometry {
	geo := smallGeometry()
	geo.MaxChannels = 1
	return geo
}

func TestHandleCreateChannel_FullStorageRejected(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnitWithGeometry(t, reg, "s1", oneChannelGeometry())
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	first := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://x/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}

	second := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 2, RTSPURL: "rtsp://x/z", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", second.StatusCode)
	}
}

func TestHandleUpdateChannel_SameStorageSkipsCapacityCheck(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnitWithGeometry(t, reg, "s1", oneChannelGeometry())
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

	// s1 is already at its MaxChannels: 1 capacity because of this very
	// channel -- an update that leaves it on s1 must not be rejected.
	update := putJSON(t, srv, "/channels/1", updateChannelRequest{
		RTSPURL: "rtsp://new/y", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer update.Body.Close()
	if update.StatusCode != http.StatusOK {
		t.Fatalf("same-storage update status = %d, want 200", update.StatusCode)
	}
}

func TestHandleUpdateChannel_DifferentFullStorageRejected(t *testing.T) {
	reg := NewStorageRegistry()
	regTestUnitWithGeometry(t, reg, "s1", smallGeometry())
	regTestUnitWithGeometry(t, reg, "s2", oneChannelGeometry())
	im := ingest.NewIngestManager()
	t.Cleanup(im.Stop)
	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	create1 := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 1, RTSPURL: "rtsp://a", Storage: "s1", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	create1.Body.Close()
	if create1.StatusCode != http.StatusCreated {
		t.Fatalf("create channel 1 status = %d, want 201", create1.StatusCode)
	}
	create2 := postJSON(t, srv, "/channels", createChannelRequest{
		ID: 2, RTSPURL: "rtsp://b", Storage: "s2", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	create2.Body.Close()
	if create2.StatusCode != http.StatusCreated {
		t.Fatalf("create channel 2 status = %d, want 201", create2.StatusCode)
	}

	// s2 is now at its MaxChannels: 1 capacity (channel 2). Moving channel 1
	// from s1 onto s2 must be rejected.
	update := putJSON(t, srv, "/channels/1", updateChannelRequest{
		RTSPURL: "rtsp://a", Storage: "s2", CapturePolicy: channelCapturePolicyRequest{Type: "continuous"},
	})
	defer update.Body.Close()
	if update.StatusCode != http.StatusConflict {
		t.Fatalf("move-to-full-storage status = %d, want 409", update.StatusCode)
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
