package farcctl_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/traycers/farc/internal/farcctl"
)

func TestClient_CreateStorage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	req := farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	}
	info, err := client.CreateStorage(ctx, req)
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}
	if info.ID != "s1" || info.Path != req.Path {
		t.Fatalf("info = %+v", info)
	}
	if _, ok := ts.reg.Get("s1"); !ok {
		t.Fatalf("storage not registered on farcd's side")
	}
}

func TestClient_CreateStorage_DuplicateIDIsAPIErrorConflict(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	req := farcctl.CreateStorageRequest{
		ID: "dup", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	}
	_, err := client.CreateStorage(ctx, req)
	if err != nil {
		t.Fatalf("first CreateStorage: %v", err)
	}

	req.Path = filepath.Join(t.TempDir(), "storage2.img")
	_, err = client.CreateStorage(ctx, req)
	if err == nil {
		t.Fatalf("second CreateStorage: want error, got nil")
	}
	var ae *farcctl.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error = %v (%T), want *farcctl.APIError", err, err)
	}
	if ae.Status != 409 {
		t.Fatalf("APIError.Status = %d, want 409", ae.Status)
	}
}

func TestClient_ListStorages(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}

	list, err := client.ListStorages(ctx)
	if err != nil {
		t.Fatalf("ListStorages: %v", err)
	}
	if len(list) != 1 || list[0].ID != "s1" {
		t.Fatalf("list = %+v, want exactly storage s1", list)
	}
}

func TestClient_RemoveStorage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}

	err = client.RemoveStorage(ctx, "s1")
	if err != nil {
		t.Fatalf("RemoveStorage: %v", err)
	}
	if _, ok := ts.reg.Get("s1"); ok {
		t.Fatalf("storage still registered after RemoveStorage")
	}
}

func TestClient_RemoveStorage_UnknownIDIsAPIErrorNotFound(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)

	err := client.RemoveStorage(context.Background(), "nope")
	var ae *farcctl.APIError
	if !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("error = %v, want *farcctl.APIError{Status: 404}", err)
	}
}

func TestClient_SetRetentionDays(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}

	err = client.SetRetentionDays(ctx, "s1", 7)
	if err != nil {
		t.Fatalf("SetRetentionDays: %v", err)
	}
	unit, ok := ts.reg.Get("s1")
	if !ok {
		t.Fatalf("storage not registered")
	}
	if got := unit.Index().RetentionDays(); got != 7 {
		t.Fatalf("RetentionDays = %d, want 7", got)
	}
}

func TestClient_CreateChannel(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}

	info, err := client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "s1",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "continuous", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if info.Channel != 1 || info.Storage != "s1" || info.PolicyType != "continuous" {
		t.Fatalf("info = %+v", info)
	}
}

func TestClient_RemoveChannel(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}
	_, err = client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "s1",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "continuous", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	err = client.RemoveChannel(ctx, 1)
	if err != nil {
		t.Fatalf("RemoveChannel: %v", err)
	}
	list, err := client.ListChannels(ctx, "")
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v, want empty after RemoveChannel", list)
	}
}

func TestClient_ListChannels_FiltersByStorage(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
			ID: id, Path: filepath.Join(t.TempDir(), id+".img"),
			Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
		})
		if err != nil {
			t.Fatalf("CreateStorage(%s): %v", id, err)
		}
	}
	_, err := client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "a",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "continuous", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	_, err = client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 2, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "b",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "continuous", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	list, err := client.ListChannels(ctx, "a")
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(list) != 1 || list[0].Channel != 1 {
		t.Fatalf("list = %+v, want exactly channel 1", list)
	}
}

func TestClient_FindChannel(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}
	_, err = client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "s1",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "event", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	info, ok, err := client.FindChannel(ctx, 1)
	if err != nil {
		t.Fatalf("FindChannel: %v", err)
	}
	if !ok || info.PolicyType != "event" {
		t.Fatalf("FindChannel = %+v, %v, want ok=true PolicyType=event", info, ok)
	}

	_, ok, err = client.FindChannel(ctx, 999)
	if err != nil {
		t.Fatalf("FindChannel: %v", err)
	}
	if ok {
		t.Fatalf("FindChannel(999) ok = true, want false")
	}
}

func TestClient_SetCapturePolicy(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}
	_, err = client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "s1",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "event", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	err = client.SetCapturePolicy(ctx, 1, farcctl.SetCapturePolicyRequest{
		Type: "event", PrerecordNS: 5, PostrecordNS: 6,
	})
	if err != nil {
		t.Fatalf("SetCapturePolicy: %v", err)
	}
	info, ok, err := client.FindChannel(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("FindChannel: ok=%v err=%v", ok, err)
	}
	if info.PrerecordNS != 5 || info.PostrecordNS != 6 {
		t.Fatalf("info = %+v, want PrerecordNS=5 PostrecordNS=6", info)
	}
}

func TestClient_StartAndStopRecording(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	client := farcctl.New(ts.URL)
	ctx := context.Background()

	_, err := client.CreateStorage(ctx, farcctl.CreateStorageRequest{
		ID: "s1", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	if err != nil {
		t.Fatalf("CreateStorage: %v", err)
	}
	_, err = client.CreateChannel(ctx, farcctl.CreateChannelRequest{
		ID: 1, RTSPURL: "rtsp://127.0.0.1:1/none", Storage: "s1",
		CapturePolicy: farcctl.CreateChannelCapturePolicy{Type: "continuous", MaxDeferredStartNS: uint64(1000)},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	err = client.StartRecording(ctx, 1, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	err = client.StopRecording(ctx, 1)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
}
