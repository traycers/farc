package msmclient_test

import (
	"context"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/api"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/msmclient"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
)

// Recreated from internal/api/testutil_test.go (an unexported _test.go
// helper, not importable across packages), same as internal/hlsclient's own
// copy (internal/hlsclient/testutil_test.go).

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

func newTestUnit(t *testing.T) *storage.Unit {
	t.Helper()
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "storage.img")
	geo := smallGeometry()

	err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
		t.Fatalf("close after init: %v", err)
	}

	b2, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	u, err := storage.Open(storage.OpenConfig{Backend: b2})
	if err != nil {
		b2.Close()
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { u.Close() })
	return u
}

func writeVideoFrame(t *testing.T, unit *storage.Unit, channels []uint16, channel uint32, begin, end uint64, frameData string, frameTime, now uint64) [16]byte {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frameTime,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	if err := f.AddFrames(configID, []fcontainer.Frame{
		{Data: []byte(frameData), Time: frameTime, Kind: mediatree.FrameKindI},
	}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	uuid, err := unit.WriteFcontainer(channels, begin, end, f, now)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	return uuid
}

func newTestServer(t *testing.T, unit *storage.Unit) (wsBase string, push *api.EventPushServer) {
	t.Helper()
	reg := api.NewStorageRegistry()
	err := reg.Register("s1", unit, "/dev/null", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push = api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, nil, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http"), push
}

// TestClient_Subscribe_ReceivesEventThenTOC exercises the exact sequence
// internal/msmd relies on: a fblock.ready "event" frame immediately followed
// by a "toc" frame carrying that fblock's real TOC bytes, both decoded into
// msmclient.Event.
func TestClient_Subscribe_ReceivesEventThenTOC(t *testing.T) {
	unit := newTestUnit(t)
	wsBase, push := newTestServer(t, unit)

	c := msmclient.New(wsBase)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give the server time to process the subscribe message before
	// publishing -- Publish is non-blocking/drop-if-slow, same reasoning
	// as internal/api/eventpush_test.go's own sibling tests.
	time.Sleep(50 * time.Millisecond)

	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "framedata", 150, 1000)
	idx, ok := unit.ResolveUUID(uuid)
	if !ok {
		t.Fatal("ResolveUUID: fblock just written not found")
	}
	push.Publish(api.JournalEvent{
		Name: api.EventFblockReady, Storage: "s1", Index: idx, UUID: hex.EncodeToString(uuid[:]), Begin: 100, End: 200,
	})

	var evEvent, tocEvent msmclient.Event
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			if ev.Type == "toc" {
				tocEvent = ev
			} else {
				evEvent = ev
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for events")
		}
	}

	if evEvent.Type != "event" || evEvent.Name != api.EventFblockReady || evEvent.Storage != "s1" ||
		evEvent.Begin != 100 || evEvent.End != 200 || !evEvent.HasUUID || evEvent.UUID != uuid {
		t.Fatalf("event frame = %+v", evEvent)
	}
	if tocEvent.Type != "toc" || tocEvent.Storage != "s1" || tocEvent.Index != idx || len(tocEvent.TOC) == 0 {
		t.Fatalf("toc frame = %+v", tocEvent)
	}
}
