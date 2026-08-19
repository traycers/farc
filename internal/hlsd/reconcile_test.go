package hlsd

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/traycers/farc/internal/hlsapi"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/segmentcache"
	"github.com/traycers/farc/internal/toccache"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/toc"
)

// fakeHLSClient is a fake hlsclient.API: reconcile's own dependency (Subscribe/
// ListChannels) is fully controllable; the three methods only
// tocindex.EventSubscriber's background goroutine touches (GetTOC/Catalog)
// just fail fast, since these tests are about channel reconciliation, not
// TOC indexing.
type fakeHLSClient struct {
	mu       sync.Mutex
	channels []hlsclient.ChannelInfo
	listErr  error
	subCh    chan hlsclient.Event
}

func newFakeHLSClient() *fakeHLSClient {
	return &fakeHLSClient{subCh: make(chan hlsclient.Event)}
}

func (f *fakeHLSClient) setChannels(cs ...hlsclient.ChannelInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = cs
}

func (f *fakeHLSClient) Subscribe(ctx context.Context, storageID string, want []string, channels []uint16, includeTOC bool) (<-chan hlsclient.Event, error) {
	return f.subCh, nil
}

func (f *fakeHLSClient) ListChannels(ctx context.Context) ([]hlsclient.ChannelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]hlsclient.ChannelInfo, len(f.channels))
	copy(out, f.channels)
	return out, nil
}

func (f *fakeHLSClient) GetTOC(ctx context.Context, storageID string, uuid [16]byte) (*toc.Columns, error) {
	return nil, errors.New("fakeHLSClient: GetTOC not available")
}

func (f *fakeHLSClient) Catalog(ctx context.Context, storageID string) ([]hlsclient.CatalogEntry, error) {
	return nil, errors.New("fakeHLSClient: Catalog not available")
}

func (f *fakeHLSClient) ReadRanges(ctx context.Context, storageID string, uuid [16]byte, ranges []hlsclient.Range) ([][]byte, error) {
	return nil, errors.New("fakeHLSClient: ReadRanges not available")
}

// newTestHlsd builds a minimal *Hlsd directly (bypassing New's config-driven
// setup, none of which channel reconciliation needs) wired to client --
// letting applyRemoteList/startChannel/stopChannel be driven and asserted on
// directly, without a real farcd or HTTP/WS server.
func newTestHlsd(t *testing.T, client hlsclient.API) *Hlsd {
	t.Helper()
	index := tocindex.NewIndex()
	tocCache, err := toccache.New(t.TempDir())
	if err != nil {
		t.Fatalf("toccache.New: %v", err)
	}
	cache, err := segmentcache.New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("segmentcache.New: %v", err)
	}
	return &Hlsd{
		index:      index,
		client:     client,
		tocCache:   tocCache,
		apiServer:  hlsapi.New(index, client, map[uint16]bool{}, cache, 10*time.Millisecond),
		configPath: filepath.Join(t.TempDir(), "hls.config.json"),
		logf:       func(string, ...any) {},
	}
}

func TestApplyRemoteList_StartsNewAndStopsGoneChannels(t *testing.T) {
	client := newFakeHLSClient()
	client.setChannels(hlsclient.ChannelInfo{Channel: 1, Storage: "disk0"})
	h := newTestHlsd(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracked := map[uint16]*trackedSub{
		2: {cancel: func() {}, storage: "disk1"}, // no longer remote -- must be stopped
	}

	err := h.applyRemoteList(ctx, tracked)
	if err != nil {
		t.Fatalf("applyRemoteList: %v", err)
	}

	if _, ok := tracked[1]; !ok {
		t.Fatal("channel 1 (remote, untracked) was not started")
	}
	if tracked[1].storage != "disk0" {
		t.Fatalf("tracked[1].storage = %q, want disk0", tracked[1].storage)
	}
	if _, ok := tracked[2]; ok {
		t.Fatal("channel 2 (tracked, no longer remote) was not stopped")
	}
}

func TestApplyRemoteList_StorageChanged_RestartsSubscription(t *testing.T) {
	client := newFakeHLSClient()
	client.setChannels(hlsclient.ChannelInfo{Channel: 1, Storage: "disk1"})
	h := newTestHlsd(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	oldCancelCalled := false
	tracked := map[uint16]*trackedSub{
		1: {cancel: func() { oldCancelCalled = true }, storage: "disk0"},
	}

	err := h.applyRemoteList(ctx, tracked)
	if err != nil {
		t.Fatalf("applyRemoteList: %v", err)
	}

	if !oldCancelCalled {
		t.Fatal("old subscription (disk0) was not cancelled on a storage move")
	}
	if tracked[1].storage != "disk1" {
		t.Fatalf("tracked[1].storage after move = %q, want disk1", tracked[1].storage)
	}
}

func TestApplyRemoteList_ListChannelsError_Propagates(t *testing.T) {
	client := newFakeHLSClient()
	client.listErr = errors.New("boom")
	h := newTestHlsd(t, client)

	err := h.applyRemoteList(context.Background(), map[uint16]*trackedSub{})
	if err == nil {
		t.Fatal("applyRemoteList = nil error, want the ListChannels failure to surface")
	}
}

func TestStartChannel_IdempotentUnderSameStorage(t *testing.T) {
	client := newFakeHLSClient()
	h := newTestHlsd(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracked := map[uint16]*trackedSub{}
	h.startChannel(ctx, tracked, 1, "disk0")
	firstSub := tracked[1]

	h.startChannel(ctx, tracked, 1, "disk0") // same storage -- must be a no-op
	if tracked[1] != firstSub {
		t.Fatal("startChannel replaced the tracked subscription for an unchanged storage")
	}
}

func TestStopChannel_RemovesFromTrackedAndIndex(t *testing.T) {
	client := newFakeHLSClient()
	h := newTestHlsd(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracked := map[uint16]*trackedSub{}
	h.startChannel(ctx, tracked, 1, "disk0")
	if _, ok := tracked[1]; !ok {
		t.Fatal("startChannel did not add channel 1 to tracked")
	}

	h.stopChannel(tracked, 1)
	if _, ok := tracked[1]; ok {
		t.Fatal("stopChannel did not remove channel 1 from tracked")
	}
	if h.ConnectedChannels() != 0 {
		t.Fatalf("ConnectedChannels after stop = %d, want 0", h.ConnectedChannels())
	}
}

func TestStopChannel_UntrackedIsNoop(t *testing.T) {
	client := newFakeHLSClient()
	h := newTestHlsd(t, client)

	h.stopChannel(map[uint16]*trackedSub{}, 99) // must not panic
}
