package ingest

import (
	"testing"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

func vf(t uint64, kind uint8) fcontainer.Frame {
	return fcontainer.Frame{Data: []byte("x"), Time: t, Kind: kind}
}

func TestFrameQueue_VideoGOPAtomicEviction(t *testing.T) {
	q := NewFrameQueue(50) // 50ns retention
	push := func(now, ts uint64, kind uint8) {
		q.Push(now, QueuedFrame{Stream: 0, Kind: fcontainer.KindVideo, Frame: vf(ts, kind)})
	}

	// GOP1: I@0, P@10, P@20. GOP2: I@30, P@40, P@50.
	push(0, 0, mediatree.FrameKindI)
	push(10, 10, mediatree.FrameKindP)
	push(20, 20, mediatree.FrameKindP)
	push(30, 30, mediatree.FrameKindI)
	push(40, 40, mediatree.FrameKindP)
	// now=60, cutoff=10: GOP1's last frame (t=20) is < cutoff(10)? No,
	// 20 >= 10, so GOP1 is NOT fully expired yet (its last frame is still
	// within the window) -- nothing should be evicted.
	push(60, 50, mediatree.FrameKindP)

	got := q.Since(0)
	if len(got) != 6 {
		t.Fatalf("after now=60 (cutoff=10), len=%d, want 6 (GOP1 not fully expired)", len(got))
	}

	// Push again at now=100 (cutoff=50): GOP1 (0,10,20) is entirely < 50,
	// safe to drop atomically. GOP2 (30,40,50) has its last frame at 50,
	// which is >= cutoff, so GOP2 must survive whole.
	q.Push(100, QueuedFrame{Stream: 0, Kind: fcontainer.KindVideo, Frame: vf(60, mediatree.FrameKindP)})
	got = q.Since(0)
	times := make([]uint64, 0, len(got))
	for _, f := range got {
		times = append(times, f.Frame.Time)
	}
	want := []uint64{30, 40, 50, 60}
	if len(times) != len(want) {
		t.Fatalf("times = %v, want %v", times, want)
	}
	for i := range want {
		if times[i] != want[i] {
			t.Fatalf("times = %v, want %v", times, want)
		}
	}
}

func TestFrameQueue_AudioEvictsIndividually(t *testing.T) {
	q := NewFrameQueue(50)
	for _, ts := range []uint64{0, 10, 20, 30, 100} {
		q.Push(ts, QueuedFrame{Stream: 1, Kind: fcontainer.KindAudio, Frame: fcontainer.Frame{Data: []byte("a"), Time: ts}})
	}
	got := q.Since(0)
	// now=100, cutoff=50: only the ts=100 frame (>=50) plus ts=30? 30<50 so evicted too.
	if len(got) != 1 || got[0].Frame.Time != 100 {
		t.Fatalf("got %v, want just [100]", got)
	}
}

func TestFrameQueue_SinceReturnsChronologicalAcrossStreams(t *testing.T) {
	q := NewFrameQueue(1000)
	q.Push(0, QueuedFrame{Stream: 0, Kind: fcontainer.KindVideo, Frame: vf(0, mediatree.FrameKindI)})
	q.Push(5, QueuedFrame{Stream: 1, Kind: fcontainer.KindAudio, Frame: fcontainer.Frame{Data: []byte("a"), Time: 5}})
	q.Push(10, QueuedFrame{Stream: 0, Kind: fcontainer.KindVideo, Frame: vf(10, mediatree.FrameKindP)})

	got := q.Since(0)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Frame.Time < got[i-1].Frame.Time {
			t.Fatalf("not chronological: %v", got)
		}
	}
}

func TestFrameQueue_SinceOlderThanRetentionReturnsWhatsAvailable(t *testing.T) {
	q := NewFrameQueue(10)
	q.Push(0, QueuedFrame{Stream: 1, Kind: fcontainer.KindAudio, Frame: fcontainer.Frame{Data: []byte("a"), Time: 0}})
	q.Push(100, QueuedFrame{Stream: 1, Kind: fcontainer.KindAudio, Frame: fcontainer.Frame{Data: []byte("a"), Time: 100}})

	got := q.Since(0) // asking from the very start, long evicted
	if len(got) != 1 || got[0].Frame.Time != 100 {
		t.Fatalf("got %v, want just the still-available frame at 100", got)
	}
}
