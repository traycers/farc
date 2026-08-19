package storageengine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ioengine"
)

// fakeIsAligned mirrors ioengine's own (unexported) alignment check --
// duplicated here so fakeBackend can enforce the same rule a real `direct`
// backend does (internal/ioengine/direct_linux.go's WriteAt).
func fakeIsAligned(v int64, align int) bool {
	return align <= 1 || v%int64(align) == 0
}

// fakeBackend is an in-memory ioengine.Backend. corrupt marks fchunk-start
// offsets whose ReadAt flips a byte, simulating a physical write that
// didn't actually make it to media.
type fakeBackend struct {
	mu      sync.Mutex
	data    []byte
	corrupt map[int64]bool
	writes  int
	reads   int
	align   int
}

func newFakeBackend(size int) *fakeBackend {
	return &fakeBackend{data: make([]byte, size), corrupt: map[int64]bool{}, align: 1}
}

func newFakeBackendAligned(size, align int) *fakeBackend {
	b := newFakeBackend(size)
	b.align = align
	return b
}

func (b *fakeBackend) WriteAt(p []byte, offset int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !fakeIsAligned(offset, b.align) || !fakeIsAligned(int64(len(p)), b.align) {
		return 0, ioengine.ErrMisaligned
	}
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
func (b *fakeBackend) Alignment() int { return b.align }
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
	joined := strings.Join(kinds, "")
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

func TestEnqueueOpenWrite_FlushesAtFchunkSize(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 8, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})

	header := []byte("0123456789abcdef") // 16 bytes
	handle := e.EnqueueOpenWrite(0, header, 0)
	handle.Append([]byte("content1")) // 8 bytes, >= FchunkSize -> triggers
	drain(e)

	want := append(append([]byte{}, header...), append([]byte("content1"), fblock.MagicTrailer[:]...)...)
	got := backend.data[:len(want)]
	if string(got) != string(want) {
		t.Fatalf("backend bytes = %q, want %q", got, want)
	}
	// Written() excludes the trailer -- it's the confirmed *content* so far,
	// the offset Segment.Close needs to start its own final tail write at
	// (overwriting the trailer), not the trailer's own bytes.
	wantWritten := int64(len(header) + len("content1"))
	if handle.Written() != wantWritten {
		t.Fatalf("Written() = %d, want %d", handle.Written(), wantWritten)
	}
}

func TestEnqueueOpenWrite_TrailerRelocatesForward(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 8, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})

	header := []byte("0123456789abcdef") // 16 bytes
	handle := e.EnqueueOpenWrite(0, header, 0)
	handle.Append([]byte("content1"))
	drain(e)

	trailerLen := len(fblock.MagicTrailer[:])
	trailerOff := int64(len(header) + 8)
	trailerBefore := append([]byte{}, backend.data[trailerOff:trailerOff+int64(trailerLen)]...)
	if string(trailerBefore) != string(fblock.MagicTrailer[:]) {
		t.Fatalf("trailer not found at expected first position %d: got %q", trailerOff, trailerBefore)
	}

	handle.Append([]byte("content2"))
	drain(e)

	// The first trailer's position must now hold real content, not magic.
	afterFirst := backend.data[trailerOff : trailerOff+int64(trailerLen)]
	if string(afterFirst) == string(fblock.MagicTrailer[:]) {
		t.Fatalf("trailer still present at old position %d after a second flush", trailerOff)
	}
	if string(afterFirst) != "content2" {
		t.Fatalf("old trailer position now holds %q, want %q", afterFirst, "content2")
	}

	// The new trailer must be at the new live end.
	newTrailerOff := trailerOff + int64(len("content2"))
	newTrailer := backend.data[newTrailerOff : newTrailerOff+int64(trailerLen)]
	if string(newTrailer) != string(fblock.MagicTrailer[:]) {
		t.Fatalf("no relocated trailer at %d: got %q", newTrailerOff, newTrailer)
	}
}

func TestEnqueueOpenWrite_SkipsWriteWhenBelowAlignment(t *testing.T) {
	backend := newFakeBackendAligned(1024, 8)
	// FchunkSize matches alignment (both 8) so the padded header (6 bytes ->
	// 8 with EnqueueOpenWrite's own alignment padding) writes as a single
	// aligned chunk -- a real backend's FchunkSize is always far larger than
	// any header (4-16 MiB vs. a few hundred bytes), so this mirrors that
	// relationship at test scale instead of the old FchunkSize(2) < header
	// size, which produced sub-alignment chunk writes no real config could.
	e := New(backend, Config{FchunkSize: 8, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})

	header := []byte("HEADER") // 6 bytes
	handle := e.EnqueueOpenWrite(0, header, 0)
	handle.Append([]byte("ab")) // 2 bytes: below alignment(8) -> writable == 0

	drain(e)
	if handle.Written() != int64(len(header)) {
		t.Fatalf("Written() = %d, want %d (nothing beyond header written while below alignment)", handle.Written(), len(header))
	}

	handle.Append([]byte("cdefgh")) // pendingAppend now 8 bytes total == alignment -> should flush
	drain(e)
	wantLen := int64(len(header) + 8) // Written() excludes the trailer
	if handle.Written() != wantLen {
		t.Fatalf("Written() = %d, want %d once alignment reached", handle.Written(), wantLen)
	}
}

// TestWriteHandle_AppendReturnsErrorAfterJobFailed covers the silent-failure
// gap from .scratch/fblocks-ui/issues/08-ingest-stalls-after-rtp-packet-loss.md:
// an open (continuous) job that fails mid-flush -- corruption here, a real
// WriteAt error in production -- gets dequeued by finishWriteLocked exactly
// like an ordinary job, but nothing was ever waiting on its ticket. Before
// this fix, a caller kept blindly calling Append forever, accumulating
// pendingAppend on an orphaned job the engine would never look at again.
func TestWriteHandle_AppendReturnsErrorAfterJobFailed(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 4, ReadChunkSize: 4, WarningAt: 100, BackpressureAt: 200})

	header := []byte("HEAD") // 4 bytes, already aligned (align=1 here)
	handle := e.EnqueueOpenWrite(0, header, 0)

	// The next flush's write-verify will fail its read-back comparison at
	// offset 4 (right after the header).
	backend.corrupt[4] = true

	if err := handle.Append([]byte("abcd")); err != nil {
		t.Fatalf("first Append: unexpected error %v", err)
	}
	drain(e) // header write succeeds, then the flush at offset 4 fails and dequeues the job

	if err := handle.Append([]byte("more")); err == nil {
		t.Fatal("Append after job failure = nil error, want non-nil")
	}
}

func TestEnqueueOpenWrite_CloseFinishesAfterLastBatch(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 100, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})

	header := []byte("HEADER")
	handle := e.EnqueueOpenWrite(0, header, 0)
	handle.Append([]byte("leftover")) // below FchunkSize(100), never triggers on its own

	ticket := handle.Close()
	drain(e)
	res, err := ticket.Wait()
	if err != nil || res.Corrupted {
		t.Fatalf("Close ticket: res=%+v err=%v", res, err)
	}
	// The leftover, never-flushed bytes are not this job's responsibility to
	// write (the caller's own final tail write covers them) -- only the
	// header is confirmed written.
	if handle.Written() != int64(len(header)) {
		t.Fatalf("Written() = %d, want %d", handle.Written(), len(header))
	}
}

func TestEnqueueOpenWrite_TimeoutTriggersWithoutFchunkSize(t *testing.T) {
	backend := newFakeBackend(1024)
	e := New(backend, Config{FchunkSize: 10_000, ReadChunkSize: 8, WarningAt: 100, BackpressureAt: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)

	header := []byte("HEADER")
	handle := e.EnqueueOpenWrite(0, header, 30*time.Millisecond)
	handle.Append([]byte("small")) // far below FchunkSize; only the timeout can trigger this

	want := int64(len(header) + len("small")) // Written() excludes the trailer
	deadline := time.Now().Add(2 * time.Second)
	for handle.Written() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if handle.Written() != want {
		t.Fatalf("Written() = %d, want %d (timeout should have flushed without FchunkSize being reached)", handle.Written(), want)
	}
}
