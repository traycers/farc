package ingest

import (
	"sort"
	"sync"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

// StreamID identifies one (stream number, kind) pair within a channel.
type StreamID struct {
	Stream uint32
	Kind   fcontainer.StreamKind
}

// QueuedFrame is one frame sitting in a FrameQueue, tagged with which
// stream it belongs to and the codec params active when it was captured
// (docs/docs/archive/10-capture-policy.md §3: replaying queued frames into
// a freshly opened segment must re-add every distinct config version they
// span, in order, before the frames themselves).
type QueuedFrame struct {
	Stream uint32
	Kind   fcontainer.StreamKind
	Frame  fcontainer.Frame
	Params *fcontainer.StreamParams
}

type streamQueue struct {
	isVideo bool
	frames  []QueuedFrame // time-ascending
}

// gopEnd returns the exclusive end index of the GOP starting at position 0
// — the position of the next I-frame, or len(frames) if there isn't one.
func (sq *streamQueue) gopEnd() int {
	for i := 1; i < len(sq.frames); i++ {
		if sq.frames[i].Frame.Kind == mediatree.FrameKindI {
			return i
		}
	}
	return len(sq.frames)
}

func (sq *streamQueue) evict(cutoff uint64) {
	for len(sq.frames) > 0 {
		if !sq.isVideo {
			if sq.frames[0].Frame.Time >= cutoff {
				return
			}
			sq.frames = sq.frames[1:]
			continue
		}
		// Video: GOP-atomic eviction — never strand a P-frame without its
		// I-frame (docs/docs/archive/10-capture-policy.md §2).
		end := sq.gopEnd()
		if sq.frames[end-1].Frame.Time >= cutoff {
			return
		}
		sq.frames = sq.frames[end:]
	}
}

// FrameQueue is one channel's frame queue: a sliding retention window,
// always populated regardless of whether a segment is currently recording
// (docs/docs/archive/10-capture-policy.md §2/§4). Safe for concurrent use.
type FrameQueue struct {
	mu      sync.Mutex
	depth   uint64 // ns, retention window
	streams map[StreamID]*streamQueue
}

// NewFrameQueue creates an empty queue retaining depth ns of history.
func NewFrameQueue(depth uint64) *FrameQueue {
	return &FrameQueue{depth: depth, streams: make(map[StreamID]*streamQueue)}
}

// Push appends qf and evicts anything now past the retention window for
// its stream. now is used only to compute the eviction cutoff — it need
// not equal qf.Frame.Time (a queue fed out of order would still evict
// correctly against wall-clock arrival, which is all §2 requires).
func (q *FrameQueue) Push(now uint64, qf QueuedFrame) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := StreamID{qf.Stream, qf.Kind}
	sq, ok := q.streams[id]
	if !ok {
		sq = &streamQueue{isVideo: qf.Kind == fcontainer.KindVideo}
		q.streams[id] = sq
	}
	sq.frames = append(sq.frames, qf)
	cutoff := uint64(0)
	if now > q.depth {
		cutoff = now - q.depth
	}
	sq.evict(cutoff)
}

// Since returns every currently-queued frame (across all streams) with
// Frame.Time >= t, in chronological order — "вытолкнуть все кадры очереди
// с меткой >= t, по порядку" (§5.1/§5.2). If t is older than what's left
// after eviction, this naturally returns everything still available — no
// separate "queue depth exceeded" case is needed.
func (q *FrameQueue) Since(t uint64) []QueuedFrame {
	q.mu.Lock()
	defer q.mu.Unlock()
	var all []QueuedFrame
	for _, sq := range q.streams {
		for _, f := range sq.frames {
			if f.Frame.Time >= t {
				all = append(all, f)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Frame.Time < all[j].Frame.Time })
	return all
}
