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
	"sync"

	"traycers/farc/internal/ioengine"
)

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

	done chan struct{}
	res  WriteResult
	err  error
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

// Step performs exactly one fchunk write-verify or one read portion,
// choosing per ADR-005/ADR-011, and reports whether it did anything (false
// means both queues are empty).
func (e *Engine) Step() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	haveWrite := len(e.writeQueue) > 0
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
	if job.pos >= int64(len(job.data)) {
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

// Run drives Step in a loop on the calling goroutine until ctx is done,
// sleeping only when both queues are empty. Intended to be started as
// StorageEngine's single disk-owning goroutine (`go engine.Run(ctx)`).
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
		for len(e.writeQueue) == 0 && len(e.readQueue) == 0 && ctx.Err() == nil {
			e.cond.Wait()
		}
		e.mu.Unlock()
	}
}

// Close releases the backend. Callers must ensure no goroutine is inside
// Run/Step when calling Close.
func (e *Engine) Close() error {
	return e.backend.Close()
}
