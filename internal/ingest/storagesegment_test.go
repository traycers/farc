package ingest

import (
	"encoding/binary"
	"testing"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// hasChannelNode reports whether elems contains a RoleChannel node with
// this exact channel value.
func hasChannelNode(elems []mediatree.Element, channel uint32) bool {
	for _, e := range elems {
		if e.Role == mediatree.RoleChannel && len(e.Value) == 4 && binary.LittleEndian.Uint32(e.Value) == channel {
			return true
		}
	}
	return false
}

func TestStorageSegment_TwoChannelsShareOneUnderlyingSegment(t *testing.T) {
	seg, backend := newTestSegment()

	cid1, err := seg.AddStreamParams(1, 1000, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams(channel 1): %v", err)
	}
	if err := seg.AddFrames(1, 1000, cid1, []fcontainer.Frame{vframe(10, mediatree.FrameKindI)}); err != nil {
		t.Fatalf("AddFrames(channel 1): %v", err)
	}

	cid2, err := seg.AddStreamParams(2, 1000, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams(channel 2): %v", err)
	}
	if err := seg.AddFrames(2, 1000, cid2, []fcontainer.Frame{vframe(20, mediatree.FrameKindI)}); err != nil {
		t.Fatalf("AddFrames(channel 2): %v", err)
	}

	if backend.beginCount != 1 {
		t.Fatalf("BeginSegment called %d times, want 1 (both channels should share the one segment)", backend.beginCount)
	}
	elems := seg.Elements()
	if !hasChannelNode(elems, 1) || !hasChannelNode(elems, 2) {
		t.Fatalf("expected both channel 1 and channel 2 nodes in the shared segment's tree")
	}
}

func TestStorageSegment_RotationReopensViaBackend(t *testing.T) {
	seg, backend := newTestSegment()

	cid, err := seg.AddStreamParams(1, 1000, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	if err := seg.AddFrames(1, 1000, cid, []fcontainer.Frame{vframe(10, mediatree.FrameKindI)}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	genBefore := seg.Generation()

	backend.rotate()

	cid2, err := seg.AddStreamParams(1, 2000, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams after rotate: %v", err)
	}
	if err := seg.AddFrames(1, 2000, cid2, []fcontainer.Frame{vframe(30, mediatree.FrameKindI)}); err != nil {
		t.Fatalf("AddFrames after rotate: %v", err)
	}

	if backend.beginCount != 2 {
		t.Fatalf("BeginSegment called %d times, want 2 (one initial + one reopen after rotate)", backend.beginCount)
	}
	if seg.Generation() != genBefore+1 {
		t.Fatalf("Generation() = %d, want %d after rotation", seg.Generation(), genBefore+1)
	}

	times := frameTimesElems(seg.Elements(), mediatree.RoleFrameTimeVideo)
	if len(times) != 1 || times[0] != 30 {
		t.Fatalf("frame times after rotation = %v, want [30] (pre-rotation nodes must be gone)", times)
	}
}
