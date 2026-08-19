package tocindex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/toccache"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/toc"
)

// newTestCache is TestEventSubscriber_BootstrapUsesCacheDiff/
// TestEventSubscriber_BootstrapEvictsStaleCacheEntry's shared fixture: a
// real on-disk toccache rooted at a fresh t.TempDir(), matching how hlsd
// actually constructs one (a real filesystem, not a fake).
func newTestCache(t *testing.T) *toccache.Cache {
	t.Helper()
	c, err := toccache.New(t.TempDir())
	if err != nil {
		t.Fatalf("toccache.New: %v", err)
	}
	return c
}

func pollUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// TestEventSubscriber_IndexesVideoPresenceSegments is the wiring seam
// between VideoPresenceSegments (pure computation) and Record.VideoSegments
// (storage): indexContainer must actually populate VideoSegments from the
// real decoded TOC, not just Begin/End, so Timeline() has something to
// aggregate (.scratch/player-redesign/issues/
// 01-hls-server-timeline-endpoint.md).
func TestEventSubscriber_IndexesVideoPresenceSegments(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelVideo(t, unit, 9, []uint64{0, second, 2 * second})

	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	idx := tocindex.NewIndex()
	cache := newTestCache(t)
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid)
		return ok
	}) {
		t.Fatalf("uuid never indexed")
	}

	segments := idx.Channel(9).Timeline(0, 3*second)
	want := []tocindex.Segment{{Begin: 0, End: 2 * second}}
	if len(segments) != 1 || segments[0] != want[0] {
		t.Fatalf("Timeline(0,3s) = %+v, want %+v", segments, want)
	}
}

// TestEventSubscriber_BootstrapThenLive writes one fcontainer before the
// subscriber ever starts (must be picked up by bootstrap, ADR-018), then
// writes enough further fcontainers to force the small (N=4) fixture
// geometry's cyclic writer to evict the very first fblock — exercising the
// live fblock.deleted path end to end, against a real farcd fixture rather
// than a fake client.
func TestEventSubscriber_BootstrapThenLive(t *testing.T) {
	unit := newTestUnit(t)
	uuid1 := writeVideoFrame(t, unit, []uint16{9}, 9, 100, 100, "f1", 100, 1000)

	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)
	idx := tocindex.NewIndex()
	cache := newTestCache(t)
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid1)
		return ok
	}) {
		t.Fatalf("bootstrap: uuid1 never indexed")
	}
	if rec, _ := idx.Channel(9).Lookup(uuid1); rec.Begin != 100 || rec.End != 100 || rec.StorageID != "s1" {
		t.Fatalf("bootstrapped record = %+v, want Begin=End=100 StorageID=s1", rec)
	}

	// Fill the remaining 3 slots, then write a 5th: geometry N=4 forces the
	// cyclic writer to reuse slot 0 (uuid1's), publishing fblock.deleted.
	writeVideoFrame(t, unit, []uint16{9}, 9, 200, 200, "f2", 200, 1001)
	writeVideoFrame(t, unit, []uint16{9}, 9, 300, 300, "f3", 300, 1002)
	writeVideoFrame(t, unit, []uint16{9}, 9, 400, 400, "f4", 400, 1003)
	uuid5 := writeVideoFrame(t, unit, []uint16{9}, 9, 500, 500, "f5", 500, 1004)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid5)
		return ok
	}) {
		t.Fatalf("live: uuid5 never indexed")
	}
	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid1)
		return !ok
	}) {
		t.Fatalf("live: uuid1 was never evicted from the index")
	}

	all := idx.Channel(9).All()
	if len(all) != 4 {
		t.Fatalf("All() = %+v, want exactly 4 records (uuid1 evicted)", all)
	}
}

// TestEventSubscriber_LiveEventSkipsGetTOC is issue 01's core contract
// (.scratch/hls-toc-bootstrap/issues/01-toc-via-ws-push.md): once
// EventSubscriber gets its TOC pushed inline with the live
// fblock.write.completed event, it must not also issue a follow-up
// GetTOC HTTP call for that same fcontainer -- the whole point being to
// remove that round trip from the steady-state hot path. Bootstrap (a
// separate, still-GetTOC-based path per issue 02) is exempt: the counter's
// baseline is taken after bootstrap completes, so only the live path is
// under test.
func TestEventSubscriber_LiveEventSkipsGetTOC(t *testing.T) {
	unit := newTestUnit(t)
	uuid1 := writeVideoFrame(t, unit, []uint16{9}, 9, 100, 100, "f1", 100, 1000)

	reg := api.NewStorageRegistry()
	if err := reg.Register("s1", unit, "/dev/null", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	apiSrv := api.NewHttpApiServer(reg, nil, push)

	var tocCalls int32
	handler := apiSrv.Handler()
	countingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/toc") {
			atomic.AddInt32(&tocCalls, 1)
		}
		handler.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(countingHandler)
	defer ts.Close()

	client := hlsclient.New(ts.URL, "ws"+strings.TrimPrefix(ts.URL, "http"))
	idx := tocindex.NewIndex()
	cache := newTestCache(t)
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid1)
		return ok
	}) {
		t.Fatalf("bootstrap: uuid1 never indexed")
	}
	baseline := atomic.LoadInt32(&tocCalls)

	uuid2 := writeVideoFrame(t, unit, []uint16{9}, 9, 200, 200, "f2", 200, 1001)
	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid2)
		return ok
	}) {
		t.Fatalf("live: uuid2 never indexed")
	}

	if got := atomic.LoadInt32(&tocCalls); got != baseline {
		t.Fatalf("GetTOC called %d time(s) while indexing a live event, want 0 (baseline %d) -- TOC should have arrived pushed over WS", got-baseline, baseline)
	}
}

// TestEventSubscriber_BootstrapUsesCacheDiff is issue 02's core contract
// (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// a uuid that's already on disk in toccache AND still live in farcd's
// catalog must not cost a GetTOC round trip during bootstrap -- only the
// delta (cache miss, but live) should. uuid1 here is pre-cached before the
// subscriber under test ever starts, simulating a restart with a warm
// cache.
func TestEventSubscriber_BootstrapUsesCacheDiff(t *testing.T) {
	unit := newTestUnit(t)
	uuid1 := writeVideoFrame(t, unit, []uint16{9}, 9, 100, 100, "f1", 100, 1000)

	reg := api.NewStorageRegistry()
	if err := reg.Register("s1", unit, "/dev/null", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	apiSrv := api.NewHttpApiServer(reg, nil, push)

	var tocCalls int32
	handler := apiSrv.Handler()
	countingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/toc") {
			atomic.AddInt32(&tocCalls, 1)
		}
		handler.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(countingHandler)
	defer ts.Close()

	client := hlsclient.New(ts.URL, "ws"+strings.TrimPrefix(ts.URL, "http"))

	// Pre-warm the cache exactly as a previous process's live-push writes
	// would have (slice below, issue 02's "cache gets written on the live
	// push path too") -- fetched via a throwaway client call that counts
	// against nothing the subscriber under test will do.
	columns, err := client.GetTOC(context.Background(), "s1", uuid1)
	if err != nil {
		t.Fatalf("prewarm GetTOC: %v", err)
	}
	buf, err := toc.Encode(columns)
	if err != nil {
		t.Fatalf("prewarm toc.Encode: %v", err)
	}
	cache := newTestCache(t)
	if err := cache.Put("s1", uuid1, buf); err != nil {
		t.Fatalf("prewarm cache.Put: %v", err)
	}
	baseline := atomic.LoadInt32(&tocCalls)

	idx := tocindex.NewIndex()
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := idx.Channel(9).Lookup(uuid1)
		return ok
	}) {
		t.Fatalf("bootstrap: uuid1 never indexed")
	}

	if got := atomic.LoadInt32(&tocCalls); got != baseline {
		t.Fatalf("bootstrap called GetTOC %d time(s) for an already-cached, still-live uuid, want 0 more (baseline %d)", got-baseline, baseline)
	}
}

// TestEventSubscriber_BootstrapEvictsStaleCacheEntry covers the other half
// of the diff: a uuid cached on disk but no longer present in farcd's
// catalog (aged out / overwritten by the cyclic writer between restarts)
// must be evicted, not served stale.
func TestEventSubscriber_BootstrapEvictsStaleCacheEntry(t *testing.T) {
	unit := newTestUnit(t)
	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)

	stale := [16]byte{0xa, 0xb, 0xc}
	cache := newTestCache(t)
	if err := cache.Put("s1", stale, []byte("stale-toc-bytes")); err != nil {
		t.Fatalf("Put stale entry: %v", err)
	}

	idx := tocindex.NewIndex()
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, cache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx)

	if !pollUntil(t, 3*time.Second, func() bool {
		_, ok := cache.Get("s1", stale)
		return !ok
	}) {
		t.Fatalf("stale cache entry was never evicted by bootstrap")
	}
}

// TestEventSubscriber_Run_ReturnsNilOnContextCancel locks in the invariant
// internal/hlsd's stopChannel relies on: cancelling Run's context always
// makes it return nil, never a logged "subscriber failed" error, whether
// that happens before Run does any work at all or while it's already deep
// in its live follow loop.
func TestEventSubscriber_Run_ReturnsNilOnContextCancel(t *testing.T) {
	t.Run("cancelled before Run starts", func(t *testing.T) {
		unit := newTestUnit(t)
		ts := newTestServer(t, unit)
		client := hlsclient.New(ts.URL, ts.wsURL)
		idx := tocindex.NewIndex()
		sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, newTestCache(t))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		errCh := make(chan error, 1)
		go func() { errCh <- sub.Run(ctx) }()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Run did not return after an already-cancelled context")
		}
	})

	t.Run("cancelled during live follow", func(t *testing.T) {
		unit := newTestUnit(t)
		ts := newTestServer(t, unit)
		client := hlsclient.New(ts.URL, ts.wsURL)
		idx := tocindex.NewIndex()
		sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx, newTestCache(t))

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- sub.Run(ctx) }()

		// Give bootstrap+subscribe time to complete and settle into follow.
		time.Sleep(200 * time.Millisecond)
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Run did not return after cancellation during follow")
		}
	})
}
