// Package storageengine implements StorageEngine: the sole owner of a
// Storage's disk channel (docs/docs/archive/02-storage.md §4.2.4). It knows
// nothing about fblock/fcontainer structure — callers give raw
// (offset, length)/(offset, data) — and does exactly two things:
//
//   - Write-verify (docs/docs/archive/04-storage-operations.md §7.3): a
//     write job is split into fchunk_size pieces; each piece is written,
//     read back, and compared before the next piece starts. The first
//     mismatch aborts the job and reports it as corrupted — choosing a new
//     physical index and retrying is the caller's job (Recorder).
//   - Read/write arbitration (ADR-005, ADR-011): a read job is split into
//     read_chunk_size portions so it can be preempted between portions.
//     Write has absolute priority, except that after every QuotaEvery (M)
//     write chunks the engine grants up to QuotaPortions (K) read portions
//     — unless the write queue is at BACKPRESSURE, in which case the quota
//     is withdrawn and write priority is absolute again (ADR-005 as-is).
//
// Step is the deterministic scheduling primitive: one call performs exactly
// one fchunk write-verify or one read portion and returns whether it did
// anything. Tests drive Step directly for exact, non-timing-dependent
// assertions; Run drives it in a loop for production use as the single
// disk-owning goroutine.
package storageengine

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ioengine"
)

// ErrWriteJobFailed is returned by WriteHandle.Append once its underlying
// job has already finished (a real I/O error, or a write-verify corruption
// mismatch) -- an open/continuous job that fails is dequeued by
// finishWriteLocked exactly like an ordinary one, but nothing else was ever
// waiting on its ticket to notice, so Append must report it explicitly
// instead of silently accumulating pendingAppend on a job the engine will
// never look at again (.scratch/fblocks-ui/issues/08-ingest-stalls-after-rtp-packet-loss.md).
var ErrWriteJobFailed = errors.New("storageengine: write job already failed")

// Level is the write-queue fill level ADR-011 schedules reads against.
type Level int

const (
	LevelNormal Level = iota
	LevelWarning
	LevelBackpressure
)

// Suggested defaults from ADR-011's own worked example ("например, M = 16
// фчанков, K = 4 порции"). Not applied automatically — callers set them
// explicitly in Config.
const (
	DefaultQuotaEvery    = 16
	DefaultQuotaPortions = 4
)

// Config are the per-Storage parameters StorageEngine needs. FchunkSize and
// ReadChunkSize come from the fblock params JSON
// (docs/docs/archive/03-storage-format.md); WarningAt/BackpressureAt are
// pending-write-job thresholds for Level; QuotaEvery/QuotaPortions are
// ADR-011's M/K.
type Config struct {
	FchunkSize     int64
	ReadChunkSize  int64
	WarningAt      int
	BackpressureAt int
	QuotaEvery     int
	QuotaPortions  int
}

// WriteResult reports the outcome of one write-verify job.
type WriteResult struct {
	// Corrupted is true if a written fchunk failed the read-back
	// comparison. The job stops at the first failure; FailedOffset is that
	// fchunk's absolute offset. err (returned alongside, not part of this
	// struct) is reserved for real I/O errors, distinct from a verify
	// mismatch.
	Corrupted    bool
	FailedOffset int64
}

type writeJob struct {
	offset int64
	data   []byte
	pos    int64

	// Open-write fields (zero value = an ordinary, fixed-size job from
	// EnqueueWrite; behavior below is unchanged for those). An open job is
	// ADR-017's periodic partial flush: content arrives incrementally via
	// Append and is physically flushed (fchunk_size reached, or timeout
	// elapsed, whichever first) as one write-verified batch followed by a
	// relocatable magic trailer, instead of all at once at EnqueueWrite
	// time.
	open          bool
	closed        bool // Close() called: finish once pos>=len(data), no more flush triggers
	trailerLen    int64
	headerPadLen  int64 // zero scratch bytes appended after headerAndMagic to reach Alignment() -- permanent, unlike trailerLen: rewinding into it to reclaim the space would leave the next write starting at an unaligned position, so it's just a small (< Alignment()) one-time waste, always excluded from Written() like the trailer is
	fchunkSize    int64
	timeout       time.Duration
	lastFlush     time.Time
	pendingAppend []byte

	done chan struct{}
	res  WriteResult
	err  error
}

// WriteHandle is returned by EnqueueOpenWrite.
type WriteHandle struct {
	e   *Engine
	job *writeJob
}

// Append supplies newContent bytes (already-encoded content, not including
// header/magic or the trailer) to be flushed by a future batch. Must not be
// called after Close. Returns ErrWriteJobFailed (or the underlying I/O
// error) if the job already finished on its own -- a real write error or a
// write-verify corruption mismatch dequeues an open job exactly like an
// ordinary one, and the caller must react (mark the fblock Bad, open a
// fresh segment) instead of continuing to append to a job the engine will
// never process again.
func (h *WriteHandle) Append(newContent []byte) error {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	select {
	case <-h.job.done:
		if h.job.err != nil {
			return h.job.err
		}
		return ErrWriteJobFailed
	default:
	}
	h.job.pendingAppend = append(h.job.pendingAppend, newContent...)
	h.e.cond.Broadcast()
	return nil
}

// Written reports content bytes (including the header/magic prefix, but
// excluding the trailer and any header alignment padding) physically
// confirmed so far.
func (h *WriteHandle) Written() int64 {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	excluded := h.job.trailerLen + h.job.headerPadLen
	if h.job.pos < excluded {
		return h.job.pos
	}
	return h.job.pos - excluded
}

// TrailerOffset returns the relative offset (from this job's own base
// offset) where the last confirmed flush's magic trailer currently starts
// -- i.e. where a caller's own final tail write must land to overwrite it.
// Unlike Written(), this INCLUDES headerPadLen (the header alignment
// padding is physically on disk permanently, per EnqueueOpenWrite's own
// doc comment, so any write positioned after the last confirmed content
// must account for it, not just for the trailer being overwritten).
func (h *WriteHandle) TrailerOffset() int64 {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return h.job.pos - h.job.trailerLen
}

// Close marks that no more Append calls will come. Once whatever's already
// in the job's data (the last confirmed flush, if any) is fully written,
// the job finishes exactly like a plain EnqueueWrite job — Close does not
// itself force one more sub-alignment flush; any leftover, never-flushed
// pendingAppend bytes are the caller's own responsibility (its final tail
// write, which overwrites the last trailer).
func (h *WriteHandle) Close() *WriteTicket {
	h.e.mu.Lock()
	h.job.closed = true
	h.e.cond.Broadcast()
	h.e.mu.Unlock()
	return &WriteTicket{job: h.job}
}

type readJob struct {
	offset int64
	length int64
	pos    int64
	buf    []byte

	done chan struct{}
	err  error
}

// WriteTicket is returned by EnqueueWrite; Wait blocks until Step has
// finished (or aborted) the job.
type WriteTicket struct{ job *writeJob }

func (t *WriteTicket) Wait() (WriteResult, error) {
	<-t.job.done
	return t.job.res, t.job.err
}

// ReadTicket is returned by EnqueueRead; Wait blocks until Step has filled
// the whole requested range (or hit an error).
type ReadTicket struct{ job *readJob }

func (t *ReadTicket) Wait() ([]byte, error) {
	<-t.job.done
	return t.job.buf, t.job.err
}

// Engine is StorageEngine. It owns backend exclusively: all disk access for
// this Storage happens through Step.
type Engine struct {
	backend ioengine.Backend
	cfg     Config

	mu         sync.Mutex
	cond       *sync.Cond
	writeQueue []*writeJob
	readQueue  []*readJob

	chunksWritten  int // fchunks written since the last quota grant
	quotaRemaining int // read portions still grantable in the current window
}

// New builds an Engine over an already-open backend.
func New(backend ioengine.Backend, cfg Config) *Engine {
	e := &Engine{backend: backend, cfg: cfg}
	e.cond = sync.NewCond(&e.mu)
	return e
}

// EnqueueWrite queues data to be written at offset via fchunk write-verify.
// It does not block on disk I/O; call Step (directly, or via Run on another
// goroutine) to make progress, then Ticket.Wait for the result.
func (e *Engine) EnqueueWrite(offset int64, data []byte) *WriteTicket {
	job := &writeJob{offset: offset, data: data, done: make(chan struct{})}
	e.mu.Lock()
	e.writeQueue = append(e.writeQueue, job)
	e.cond.Broadcast()
	e.mu.Unlock()
	return &WriteTicket{job: job}
}

// EnqueueOpenWrite starts a periodic-flush job at offset (the fblock's own
// absolute base offset — writes chunk by raw byte-offset-from-fblock-start,
// regardless of logical section boundaries, docs/docs/archive/
// 03-storage-format.md §10). headerAndMagic is written immediately as the
// job's first bytes. The engine itself decides *when* to physically flush
// pending Append data (fchunk_size accumulated since the last flush, OR
// timeout elapsed, whichever first — ADR-017 assigns this comparison to
// StorageEngine, not the caller) and appends a magic trailer
// (fblock.EncodeTrailer) after every flushed batch, relocating it forward
// each time. timeout <= 0 disables the timeout trigger (fchunk_size-only
// pacing — used under backlog, per the ticket's "T ignored entirely while
// catching up" decision).
func (e *Engine) EnqueueOpenWrite(offset int64, headerAndMagic []byte, timeout time.Duration) *WriteHandle {
	e.mu.Lock()
	defer e.mu.Unlock()

	data := append([]byte(nil), headerAndMagic...)
	alignment := e.alignmentLocked()
	var padLen int64
	if rem := int64(len(data)) % alignment; rem != 0 {
		padLen = alignment - rem
		data = append(data, make([]byte, padLen)...)
	}
	job := &writeJob{
		offset:       offset,
		data:         data,
		open:         true,
		headerPadLen: padLen,
		fchunkSize:   e.cfg.FchunkSize,
		timeout:      timeout,
		lastFlush:    time.Now(),
		done:         make(chan struct{}),
	}
	e.writeQueue = append(e.writeQueue, job)
	e.cond.Broadcast()
	return &WriteHandle{e: e, job: job}
}

// EnqueueRead queues a read of length bytes starting at offset.
func (e *Engine) EnqueueRead(offset, length int64) *ReadTicket {
	job := &readJob{offset: offset, length: length, buf: make([]byte, length), done: make(chan struct{})}
	e.mu.Lock()
	e.readQueue = append(e.readQueue, job)
	e.cond.Broadcast()
	e.mu.Unlock()
	return &ReadTicket{job: job}
}

// Level reports the current write-queue fill level (ADR-011), based on the
// number of write jobs not yet fully written.
func (e *Engine) Level() Level {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.levelLocked()
}

// QueueDepth reports the number of write jobs not yet fully written
// (farc_write_queue_depth, docs/docs/archive/02-storage.md §8).
func (e *Engine) QueueDepth() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.writeQueue)
}

func (e *Engine) levelLocked() Level {
	n := len(e.writeQueue)
	switch {
	case n >= e.cfg.BackpressureAt:
		return LevelBackpressure
	case n >= e.cfg.WarningAt:
		return LevelWarning
	default:
		return LevelNormal
	}
}

// writeActionableLocked reports whether the write queue's head job has
// something Step can actually do right now: an ordinary job is always
// actionable until finished; an open job is actionable while a previously
// confirmed flush is still being written, once Close has been called (it
// only needs to finish), or once its flush trigger (fchunk_size or
// timeout) fires with at least one alignment unit ready. An open job
// sitting idle below its trigger is not actionable — Step must report
// false for it rather than busy-spin, so Run can sleep (bounded by the
// timeout) instead.
func (e *Engine) writeActionableLocked() bool {
	if len(e.writeQueue) == 0 {
		return false
	}
	job := e.writeQueue[0]
	if !job.open {
		return true
	}
	if job.pos < int64(len(job.data)) {
		return true
	}
	if job.closed {
		return true
	}
	return e.flushTriggerReadyLocked(job)
}

// flushTriggerReadyLocked reports whether job's pending content should be
// physically flushed now: fchunk_size worth accumulated, or (if job.timeout
// > 0) the timeout has elapsed since the last flush — but only if at least
// one alignment unit is actually writable (ADR-017: "если writable == 0,
// ничего не пишем, остаток продолжает копиться").
func (e *Engine) flushTriggerReadyLocked(job *writeJob) bool {
	if len(job.pendingAppend) == 0 {
		return false
	}
	alignment := e.alignmentLocked()
	writable := (int64(len(job.pendingAppend)) / alignment) * alignment
	if writable <= 0 {
		return false
	}
	if int64(len(job.pendingAppend)) >= job.fchunkSize {
		return true
	}
	return job.timeout > 0 && time.Since(job.lastFlush) >= job.timeout
}

func (e *Engine) alignmentLocked() int64 {
	a := int64(e.backend.Alignment())
	if a <= 0 {
		return 1
	}
	return a
}

// applyFlushLocked physically extends job.data with the next writable batch
// of pendingAppend content plus a relocated magic trailer, as one combined
// unit for the normal write-verify loop below to pick up — the trailer
// from the previous flush (if any) is trimmed off first and job.pos
// rewound to match, so the write-verify loop overwrites it rather than
// appending after it (only the single most recent trailer ever exists on
// disk).
func (e *Engine) applyFlushLocked(job *writeJob) {
	alignment := e.alignmentLocked()
	writable := (int64(len(job.pendingAppend)) / alignment) * alignment
	if job.trailerLen > 0 {
		job.data = job.data[:int64(len(job.data))-job.trailerLen]
		job.pos -= job.trailerLen
	}
	job.data = append(job.data, job.pendingAppend[:writable]...)
	trailer := fblock.EncodeTrailer(int(alignment))
	job.data = append(job.data, trailer...)
	job.trailerLen = int64(len(trailer))
	job.pendingAppend = job.pendingAppend[writable:]
	job.lastFlush = time.Now()
}

// Step performs exactly one fchunk write-verify or one read portion,
// choosing per ADR-005/ADR-011, and reports whether it did anything (false
// means there's nothing actionable in either queue right now).
func (e *Engine) Step() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	haveWrite := e.writeActionableLocked()
	haveRead := len(e.readQueue) > 0
	if !haveWrite && !haveRead {
		return false
	}

	level := e.levelLocked()
	if level == LevelBackpressure {
		// ADR-011: quota withdrawn under backpressure, write priority
		// absolute again. Also stop accruing toward a future grant, so a
		// grant earned mid-backpressure can't leak into the step right
		// after the queue drops back below threshold.
		e.quotaRemaining = 0
		e.chunksWritten = 0
	}

	useQuota := haveWrite && haveRead && level != LevelBackpressure && e.quotaRemaining > 0
	switch {
	case useQuota:
		e.quotaRemaining--
		e.stepReadLocked()
	case haveWrite:
		e.stepWriteLocked(level)
	default: // reads only, nothing to arbitrate against
		e.stepReadLocked()
	}
	return true
}

func (e *Engine) stepWriteLocked(level Level) {
	job := e.writeQueue[0]

	if job.open && job.pos >= int64(len(job.data)) {
		// The previous flush (if any) is fully confirmed. An open job never
		// finishes just because it caught up -- only Close (finish now) or
		// a fired flush trigger (extend job.data, then fall through to the
		// normal write-verify below) make progress here.
		if job.closed {
			e.finishWriteLocked(job, WriteResult{}, nil)
			return
		}
		if !e.flushTriggerReadyLocked(job) {
			return
		}
		e.applyFlushLocked(job)
	}

	chunkLen := e.cfg.FchunkSize
	if remaining := int64(len(job.data)) - job.pos; remaining < chunkLen {
		chunkLen = remaining
	}
	off := job.offset + job.pos
	buf := job.data[job.pos : job.pos+chunkLen]

	_, err := e.backend.WriteAt(buf, off)
	if err != nil {
		e.finishWriteLocked(job, WriteResult{}, err)
		return
	}
	readBack := make([]byte, chunkLen)
	_, err = e.backend.ReadAt(readBack, off)
	if err != nil {
		e.finishWriteLocked(job, WriteResult{}, err)
		return
	}
	if !bytes.Equal(buf, readBack) {
		e.finishWriteLocked(job, WriteResult{Corrupted: true, FailedOffset: off}, nil)
		return
	}

	job.pos += chunkLen
	if level != LevelBackpressure && e.cfg.QuotaEvery > 0 {
		e.chunksWritten++
		if e.chunksWritten >= e.cfg.QuotaEvery {
			e.chunksWritten = 0
			e.quotaRemaining = e.cfg.QuotaPortions
		}
	}
	if job.pos >= int64(len(job.data)) && (!job.open || job.closed) {
		e.finishWriteLocked(job, WriteResult{}, nil)
	}
}

func (e *Engine) finishWriteLocked(job *writeJob, res WriteResult, err error) {
	job.res = res
	job.err = err
	close(job.done)
	e.writeQueue = e.writeQueue[1:]
}

func (e *Engine) stepReadLocked() {
	job := e.readQueue[0]
	portion := e.cfg.ReadChunkSize
	if remaining := job.length - job.pos; remaining < portion {
		portion = remaining
	}
	off := job.offset + job.pos
	n, err := e.backend.ReadAt(job.buf[job.pos:job.pos+portion], off)
	if err != nil {
		job.err = err
		close(job.done)
		e.readQueue = e.readQueue[1:]
		return
	}
	job.pos += int64(n)
	if job.pos >= job.length {
		close(job.done)
		e.readQueue = e.readQueue[1:]
	}
}

// nextDeadlineLocked returns the time at which the head write job's open,
// not-yet-triggered flush timeout will fire, if any is pending. Only one
// open job ever exists per Engine (one active segment at a time), so this
// is a single deadline, not a heap.
func (e *Engine) nextDeadlineLocked() (time.Time, bool) {
	if len(e.writeQueue) == 0 {
		return time.Time{}, false
	}
	job := e.writeQueue[0]
	if !job.open || job.closed || job.timeout <= 0 || len(job.pendingAppend) == 0 {
		return time.Time{}, false
	}
	if job.pos < int64(len(job.data)) {
		return time.Time{}, false // still writing the previous batch; Step keeps this busy without a timer
	}
	return job.lastFlush.Add(job.timeout), true
}

// waitUntilLocked blocks on e.cond (releasing e.mu meanwhile, per
// sync.Cond's contract) until either a broadcast arrives or deadline
// passes, whichever first — used so a quiet, low-bitrate open job's
// timeout still fires with zero new Append/EnqueueWrite calls.
func (e *Engine) waitUntilLocked(deadline time.Time) {
	timer := time.AfterFunc(time.Until(deadline), func() {
		e.mu.Lock()
		e.cond.Broadcast()
		e.mu.Unlock()
	})
	defer timer.Stop()
	e.cond.Wait()
}

// Run drives Step in a loop on the calling goroutine until ctx is done,
// sleeping only when there's nothing actionable in either queue (bounded by
// an open write job's pending flush timeout, if any). Intended to be
// started as StorageEngine's single disk-owning goroutine
// (`go engine.Run(ctx)`).
func (e *Engine) Run(ctx context.Context) {
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.cond.Broadcast()
			e.mu.Unlock()
		case <-stopped:
		}
	}()
	defer close(stopped)

	for {
		if ctx.Err() != nil {
			return
		}
		if e.Step() {
			continue
		}
		e.mu.Lock()
		for !e.writeActionableLocked() && len(e.readQueue) == 0 && ctx.Err() == nil {
			if deadline, ok := e.nextDeadlineLocked(); ok {
				e.waitUntilLocked(deadline)
			} else {
				e.cond.Wait()
			}
		}
		e.mu.Unlock()
	}
}

// Close releases the backend. Callers must ensure no goroutine is inside
// Run/Step when calling Close.
func (e *Engine) Close() error {
	return e.backend.Close()
}
