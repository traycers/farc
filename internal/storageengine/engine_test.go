package storageengine

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeBackend is an in-memory ioengine.Backend. corrupt marks fchunk-start
// offsets whose ReadAt flips a byte, simulating a physical write that
// didn't actually make it to media.
type fakeBackend struct {
	mu      sync.Mutex
	data    []byte
	corrupt map[int64]bool
	writes  int
	reads   int
}

func newFakeBackend(size int) *fakeBackend {
	return &fakeBackend{data: make([]byte, size), corrupt: map[int64]bool{}}
}

func (b *fakeBackend) WriteAt(p []byte, offset int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes++
	copy(b.data[offset:], p)
	return len(p), nil
}

func (b *fakeBackend) ReadAt(p []byte, offset int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	n := copy(p, b.data[offset:offset+int64(len(p))])
	if b.corrupt[offset] && n > 0 {
		p[0] ^= 0xFF
	}
	return n, nil
}

func (b *fakeBackend) Sync() error    { return nil }
func (b *fakeBackend) Alignment() int { return 1 }
func (b *fakeBackend) Name() string   { return "fake" }
func (b *fakeBackend) Close() error   { return nil }

func drain(e *Engine) {
	for e.Step() {
	}
}

func TestWriteVerify_RoundTrip(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 100, BackpressureAt: 200, QuotaEvery: 16, QuotaPortions: 4})

	data := []byte("abcdefgh") // 2 fchunks of 4
	ticket := e.EnqueueWrite(0, data)
	drain(e)
	res, err := ticket.Wait()
	if err != nil || res.Corrupted {
		t.Fatalf("want clean write, got res=%+v err=%v", res, err)
	}

	rt := e.EnqueueRead(0, 8)
	drain(e)
	got, err := rt.Wait()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("read back %q, want %q", got, data)
	}
}

func TestWriteVerify_DetectsCorruptionAndAbortsJob(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 100, BackpressureAt: 200, QuotaEvery: 16, QuotaPortions: 4})

	// Second fchunk (offset 4) will fail its read-back comparison.
	backend.corrupt[4] = true

	data := []byte("abcdefgh") // 2 fchunks of 4: [0,4) then [4,8)
	ticket := e.EnqueueWrite(0, data)
	drain(e)
	res, err := ticket.Wait()
	if err != nil {
		t.Fatalf("unexpected I/O error: %v", err)
	}
	if !res.Corrupted || res.FailedOffset != 4 {
		t.Fatalf("want Corrupted at offset 4, got %+v", res)
	}
	writesAfterAbort := backend.writes
	if writesAfterAbort != 2 {
		t.Fatalf("job should stop at the failing fchunk (2 writes: good + bad), got %d", writesAfterAbort)
	}

	// Retry on a new physical index — same fake backend, offset far from
	// the corrupt one — must succeed cleanly (mirrors Recorder's real
	// retry-on-new-index behavior, which lives above this package).
	retry := e.EnqueueWrite(100, data)
	drain(e)
	res2, err2 := retry.Wait()
	if err2 != nil || res2.Corrupted {
		t.Fatalf("retry at a clean offset should succeed, got res=%+v err=%v", res2, err2)
	}
}

func TestQuota_GrantsReadsEveryMWriteChunksInNormalMode(t *testing.T) {
	backend := newFakeBackend(10000)
	// M=2, K=1: after every 2 write-verified fchunks, allow 1 read portion.
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 1000, BackpressureAt: 1000, QuotaEvery: 2, QuotaPortions: 1})

	// 6 single-chunk write jobs queued up front (a saturated write queue),
	// plus 3 read jobs.
	var writes []*WriteTicket
	for i := 0; i < 6; i++ {
		writes = append(writes, e.EnqueueWrite(int64(i*4), []byte{1, 2, 3, 4}))
	}
	var reads []*ReadTicket
	for i := 0; i < 3; i++ {
		reads = append(reads, e.EnqueueRead(int64(i*4), 4))
	}

	var kinds []string
	for {
		wPending, rPending := len(e.writeQueue), len(e.readQueue)
		if wPending == 0 && rPending == 0 {
			break
		}
		wBefore := wPending
		e.Step()
		if len(e.writeQueue) < wBefore {
			kinds = append(kinds, "W")
		} else {
			kinds = append(kinds, "R")
		}
	}

	// Expect: WW R WW R WW R (2 writes, 1 read, repeating) since M=2,K=1
	// and there are exactly enough reads (3) to be granted at each of the
	// 3 quota windows produced by 6 writes.
	want := "WWRWWRWWR"
	joined := ""
	for _, k := range kinds {
		joined += k
	}
	if joined != want {
		t.Fatalf("step order = %q, want %q", joined, want)
	}

	for _, wt := range writes {
		if res, err := wt.Wait(); err != nil || res.Corrupted {
			t.Fatalf("write failed: res=%+v err=%v", res, err)
		}
	}
	for _, rt := range reads {
		if _, err := rt.Wait(); err != nil {
			t.Fatalf("read failed: %v", err)
		}
	}
}

func TestQuota_DisabledUnderBackpressure(t *testing.T) {
	backend := newFakeBackend(10000)
	// BackpressureAt=2: two or more queued write jobs puts us into
	// backpressure, where the M/K quota must never fire.
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 2, BackpressureAt: 2, QuotaEvery: 1, QuotaPortions: 10})

	for i := 0; i < 4; i++ {
		e.EnqueueWrite(int64(i*4), []byte{1, 2, 3, 4})
	}
	e.EnqueueRead(0, 4)

	if got := e.Level(); got != LevelBackpressure {
		t.Fatalf("Level() = %v, want LevelBackpressure", got)
	}

	// Drain every write chunk; the read must never be serviced in between,
	// since quota is withdrawn under backpressure (ADR-011).
	for len(e.writeQueue) > 0 {
		wBefore := len(e.writeQueue)
		e.Step()
		if len(e.writeQueue) == wBefore {
			t.Fatalf("expected a write step while writes are pending under backpressure")
		}
	}
	if len(e.readQueue) != 1 {
		t.Fatalf("read should still be pending after writes drained under backpressure, len=%d", len(e.readQueue))
	}

	// Now that writes are drained, the read proceeds normally.
	drain(e)
	if len(e.readQueue) != 0 {
		t.Fatal("read should have completed once writes drained")
	}
}

func TestLevel_Thresholds(t *testing.T) {
	backend := newFakeBackend(100)
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 2, BackpressureAt: 4, QuotaEvery: 16, QuotaPortions: 4})

	if got := e.Level(); got != LevelNormal {
		t.Fatalf("empty queue: Level() = %v, want Normal", got)
	}
	e.EnqueueWrite(0, []byte{1, 2, 3, 4})
	if got := e.Level(); got != LevelNormal {
		t.Fatalf("1 pending: Level() = %v, want Normal", got)
	}
	e.EnqueueWrite(4, []byte{1, 2, 3, 4})
	if got := e.Level(); got != LevelWarning {
		t.Fatalf("2 pending: Level() = %v, want Warning", got)
	}
	e.EnqueueWrite(8, []byte{1, 2, 3, 4})
	e.EnqueueWrite(12, []byte{1, 2, 3, 4})
	if got := e.Level(); got != LevelBackpressure {
		t.Fatalf("4 pending: Level() = %v, want Backpressure", got)
	}
}

func TestRun_DrivesQueuedWork(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 100, BackpressureAt: 200, QuotaEvery: 16, QuotaPortions: 4})

	ctx, cancel := context.WithCancel(context.Background())
	go e.Run(ctx)
	defer cancel()

	ticket := e.EnqueueWrite(0, []byte("abcdefgh"))
	select {
	case <-ticket.job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not drive the write to completion")
	}
	res, err := ticket.Wait()
	if err != nil || res.Corrupted {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
