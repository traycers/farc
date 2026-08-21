package apid_test

import (
	"context"
	"testing"

	"github.com/traycers/farc/internal/apid"
)

func TestFarcdClient_CreateListUpdateRemoveChannel(t *testing.T) {
	unit := newTestUnit(t)
	ts := newFarcdTestServer(t, unit)
	client := apid.NewFarcdClient(ts.URL)
	ctx := context.Background()

	created, err := client.CreateChannel(ctx, apid.CreateChannelRequest{
		ID:                1,
		RTSPURL:           "rtsp://mediamtx:8554/1",
		Storage:           "s1",
		CapturePolicyType: "continuous",
		Name:              "front door",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if created.Channel != 1 || created.RTSPURL != "rtsp://mediamtx:8554/1" || created.Storage != "s1" || created.Name != "front door" {
		t.Fatalf("CreateChannel result = %+v", created)
	}

	list, err := client.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(list) != 1 || list[0].Channel != 1 {
		t.Fatalf("ListChannels = %+v", list)
	}

	updated, err := client.UpdateChannel(ctx, 1, apid.UpdateChannelRequest{
		RTSPURL:           "rtsp://mediamtx:8554/1",
		Storage:           "s1",
		CapturePolicyType: "continuous",
		Name:              "front door (renamed)",
	})
	if err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	if updated.Name != "front door (renamed)" {
		t.Fatalf("UpdateChannel result = %+v", updated)
	}

	err = client.RemoveChannel(ctx, 1)
	if err != nil {
		t.Fatalf("RemoveChannel: %v", err)
	}

	list, err = client.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels after remove: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListChannels after remove = %+v, want empty", list)
	}
}

func TestFarcdClient_RemoveChannel_AlreadyGoneIsNotAnError(t *testing.T) {
	unit := newTestUnit(t)
	ts := newFarcdTestServer(t, unit)
	client := apid.NewFarcdClient(ts.URL)
	ctx := context.Background()

	// Channel 1 was never created -- removing an already-absent channel
	// must be idempotent (.scratch/live-page/issues/01-apid-server.md's
	// no-rollback/idempotent-retry design), not an error.
	err := client.RemoveChannel(ctx, 1)
	if err != nil {
		t.Fatalf("RemoveChannel of an absent channel: %v, want nil (idempotent)", err)
	}
}

func TestFarcdClient_CreateChannel_ErrorSurfacesFarcdMessage(t *testing.T) {
	unit := newTestUnit(t)
	ts := newFarcdTestServer(t, unit)
	client := apid.NewFarcdClient(ts.URL)
	ctx := context.Background()

	_, err := client.CreateChannel(ctx, apid.CreateChannelRequest{
		ID:                1,
		RTSPURL:           "rtsp://mediamtx:8554/1",
		Storage:           "does-not-exist",
		CapturePolicyType: "continuous",
	})
	if err == nil {
		t.Fatalf("CreateChannel: want error for unknown storage, got nil")
	}
}
