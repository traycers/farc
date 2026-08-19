package hlsclient_test

import (
	"context"
	"testing"
	"time"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
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

// TestClient_Catalog is the client side of issue 02's diff-based bootstrap
// (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// a bulk GET .../catalog result decodes into one entry per fblock, with
// uuid/begin/end only populated for a Ready fblock -- exactly the shape a
// caller needs to diff against a local toccache without any per-index
// requests.
func TestClient_Catalog(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx := context.Background()

	idx, ok := unit.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}

	entries, err := client.Catalog(ctx, "s1")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}

	var found *hlsclient.CatalogEntry
	for i := range entries {
		if entries[i].Index == idx {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("catalog entries = %+v, want an entry for index %d", entries, idx)
	}
	if found.State != "ready" {
		t.Fatalf("State = %q, want %q", found.State, "ready")
	}
	if found.UUID != uuid || found.Begin != 100 || found.End != 200 {
		t.Fatalf("entry = %+v, want uuid=%x begin=100 end=200", found, uuid)
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

func TestClient_ListChannels(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit, 1, 2)
	client := hlsclient.New(ts.URL, ts.wsURL)

	channels, err := client.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("channels = %+v, want exactly 2", channels)
	}
	byID := make(map[uint16]string, len(channels))
	for _, c := range channels {
		byID[c.Channel] = c.Storage
	}
	if byID[1] != "s1" || byID[2] != "s1" {
		t.Fatalf("channels = %+v, want {1:s1, 2:s1}", channels)
	}
}

// TestClient_Subscribe_GlobalChannelEvents mirrors TestClient_Subscribe for
// a global (storageID == "") subscription -- channel-lifecycle events
// rather than a per-storage fblock event.
func TestClient_Subscribe_GlobalChannelEvents(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := client.Subscribe(ctx, "", []string{hlsclient.EventChannelCreated}, nil, false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	ts.push.Publish(api.JournalEvent{Name: api.EventChannelCreated, Channel: 7, Storage: "disk0"})

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatalf("events channel closed unexpectedly")
		}
		if ev.Name != hlsclient.EventChannelCreated || ev.Channel != 7 || ev.Storage != "disk0" {
			t.Fatalf("event = %+v, want channel.created for channel 7/disk0", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for push event")
	}
}

// TestClient_Subscribe_IncludeTOC mirrors internal/msmclient's
// TestClient_Subscribe_ReceivesEventThenTOC, but for hlsclient's per-storage
// Subscribe (the shape internal/tocindex.EventSubscriber actually uses) --
// requesting includeTOC=true must surface the pushed "toc" frame's bytes on
// Event.TOC, decodable as the real TOC of the fblock that was written.
func TestClient_Subscribe_IncludeTOC(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := client.Subscribe(ctx, "s1", []string{storage.EventFblockWriteCompleted}, nil, true)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	uuid := writeVideoFrame(t, unit, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)

	var ev hlsclient.Event
	select {
	case got, ok := <-events:
		if !ok {
			t.Fatalf("events channel closed unexpectedly")
		}
		ev = got
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for event frame")
	}
	if ev.Name != storage.EventFblockWriteCompleted || !ev.HasUUID || ev.UUID != uuid {
		t.Fatalf("event = %+v, want completed event for uuid %x", ev, uuid)
	}

	var tocEv hlsclient.Event
	select {
	case got, ok := <-events:
		if !ok {
			t.Fatalf("events channel closed unexpectedly")
		}
		tocEv = got
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for toc frame")
	}
	if tocEv.Type != "toc" || len(tocEv.TOC) == 0 {
		t.Fatalf("toc event = %+v, want a populated Type=toc frame", tocEv)
	}
	columns, err := toc.Decode(tocEv.TOC)
	if err != nil {
		t.Fatalf("decode pushed toc: %v", err)
	}
	if columns.N == 0 {
		t.Fatal("decoded toc has no rows")
	}
}

func TestClient_Subscribe(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := client.Subscribe(ctx, "s1", []string{storage.EventFblockWriteCompleted}, nil, false)
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
