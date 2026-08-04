package hlsclient_test

import (
	"context"
	"testing"
	"time"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

func TestClient_CandidatesAndResolve(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx := context.Background()

	cands, err := client.Candidates(ctx, "s1", 1, 0, 1000)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v, want exactly one", cands)
	}
	if cands[0].UUID != uuid || cands[0].Begin != 100 || cands[0].End != 200 {
		t.Fatalf("candidate = %+v, want uuid=%x begin=100 end=200", cands[0], uuid)
	}

	frames, err := client.Resolve(ctx, "s1", 1, 0, 1000)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %+v, want exactly one", frames)
	}
	f := frames[0]
	if f.UUID != uuid || f.Time != 100 || string(f.Data) != "frame-a" {
		t.Fatalf("frame = %+v, want uuid=%x time=100 data=frame-a", f, uuid)
	}
	if f.Kind == nil || *f.Kind != mediatree.FrameKindI {
		t.Fatalf("frame.Kind = %v, want FrameKindI", f.Kind)
	}
}

func TestClient_GetTOCAndReadRanges(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx := context.Background()

	columns, err := client.GetTOC(ctx, "s1", uuid)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if columns.N == 0 {
		t.Fatalf("columns.N = 0, want a populated tree")
	}

	dataIDs := toc.ScanByRole(columns, mediatree.RoleFrameDataVideo)
	if len(dataIDs) != 1 {
		t.Fatalf("frame data nodes = %d, want exactly one", len(dataIDs))
	}
	offset, size, ok := toc.ContentOffset(columns, dataIDs[0])
	if !ok {
		t.Fatalf("ContentOffset: not a variable-width node")
	}

	bufs, err := client.ReadRanges(ctx, "s1", uuid, []hlsclient.Range{{Offset: offset, Size: size}})
	if err != nil {
		t.Fatalf("ReadRanges: %v", err)
	}
	if len(bufs) != 1 || string(bufs[0]) != "frame-a" {
		t.Fatalf("bufs = %v, want [\"frame-a\"]", bufs)
	}
}

func TestClient_Subscribe(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := client.Subscribe(ctx, "s1", []string{storage.EventFblockWriteCompleted}, nil)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give ServeHTTP time to read the subscribe message and register with
	// NotificationBus before the write happens — the same race
	// internal/api/eventpush_test.go guards against with an identical sleep.
	time.Sleep(50 * time.Millisecond)
	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatalf("events channel closed unexpectedly")
		}
		if ev.Name != storage.EventFblockWriteCompleted {
			t.Fatalf("event = %+v, want name %s", ev, storage.EventFblockWriteCompleted)
		}
		if !ev.HasUUID || ev.UUID != uuid {
			t.Fatalf("event = %+v, want uuid %x", ev, uuid)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for push event")
	}
}
