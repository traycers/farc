package tocindex_test

import (
	"context"
	"testing"
	"time"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/tocindex"
)

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
	sub := tocindex.NewEventSubscriber(client, "s1", []uint16{9}, idx)

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
