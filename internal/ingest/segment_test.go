package ingest

import (
	"encoding/binary"
	"sort"
	"testing"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

func channelNumbersIn(f *fcontainer.Filler, role mediatree.Role) []uint16 {
	var out []uint16
	for _, e := range f.Elements() {
		if e.Role == role {
			out = append(out, uint16(binary.LittleEndian.Uint32(e.Value)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TestSharedSegment_TwoChannelsShareOneFcontainer is the core claim of this
// round's change (docs/docs/archive/adr/014-channel-registry.md: one
// fcontainer commonly holds every channel of a storage at once): two
// channels of the same storage, both actively recording, write into the
// SAME Filler -- one WriteFcontainer call covering both, not two separate
// ones. It also covers sharedSegment.detach's "only flush once nothing is
// left active" rule, since that's the only way two channels' contributions
// can ever land in one write in the first place.
func TestSharedSegment_TwoChannelsShareOneFcontainer(t *testing.T) {
	rec := &fakeRecorder{}
	seg := newTestSegment(rec)
	pa := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	pb := NewCapturePolicy(2, seg, 1000, PolicyContinuous, PolicyParams{})

	pa.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	pb.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := pa.StartRecording(100, nil); err != nil {
		t.Fatalf("pa.StartRecording: %v", err)
	}
	if err := pb.StartRecording(100, nil); err != nil {
		t.Fatalf("pb.StartRecording: %v", err)
	}
	if err := pa.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("pa.HandleFrame: %v", err)
	}
	if err := pb.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindI)); err != nil {
		t.Fatalf("pb.HandleFrame: %v", err)
	}

	// Channel 1 stops, but channel 2 is still actively recording into the
	// same shared Filler -- must not cut its data off by flushing early.
	if err := pa.StopRecording(150); err != nil {
		t.Fatalf("pa.StopRecording: %v", err)
	}
	if len(rec.writes) != 0 {
		t.Fatalf("writes = %d, want 0 (channel 2 is still active)", len(rec.writes))
	}

	// Channel 2 was the last one left -- now it flushes, covering both
	// channels' contributions in one fcontainer.
	if err := pb.StopRecording(200); err != nil {
		t.Fatalf("pb.StopRecording: %v", err)
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1 (last channel detached)", len(rec.writes))
	}
	w := rec.writes[0]
	sortedChannels := append([]uint16(nil), w.channels...)
	sort.Slice(sortedChannels, func(i, j int) bool { return sortedChannels[i] < sortedChannels[j] })
	if len(sortedChannels) != 2 || sortedChannels[0] != 1 || sortedChannels[1] != 2 {
		t.Fatalf("channels = %v, want [1 2]", w.channels)
	}
	if w.begin != 110 || w.end != 120 {
		t.Fatalf("begin/end = %d/%d, want 110/120 (union of both channels' frame times)", w.begin, w.end)
	}
	if got := channelNumbersIn(w.filler, mediatree.RoleChannel); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("channel branches in tree = %v, want [1 2]", got)
	}
	if countRole(w.filler, mediatree.RoleFrameVideo) != 2 {
		t.Fatalf("frame(video) count = %d, want 2 (one per channel)", countRole(w.filler, mediatree.RoleFrameVideo))
	}
}

// TestSharedSegment_SizeTriggeredAutoFlush verifies the size trigger
// (sharedSegment's doc comment) fires on its own, with no channel ever
// stopping -- and that the segment transparently reopens a fresh Filler
// afterwards, so the still-"recording" channel's very next frame just
// works.
func TestSharedSegment_SizeTriggeredAutoFlush(t *testing.T) {
	rec := &fakeRecorder{}
	seg := newSharedSegment(rec, 1) // flush as soon as there's any real content at all
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := p.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}

	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1 (size-triggered auto-flush)", len(rec.writes))
	}

	// The channel is still marked recording -- its next frame must still
	// succeed against the freshly (lazily) reopened Filler, and immediately
	// trigger its own auto-flush too, since the threshold is still 1 byte.
	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame after auto-flush: %v", err)
	}
	if len(rec.writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(rec.writes))
	}
}

// TestSharedSegment_FlushInvalidatesOtherChannelsCachedConfig is the
// correctness-critical case sharedSegment's generation counter exists for:
// channel A and B both cache a configID against the segment's current
// Filler; the segment then flushes (as it would from the size trigger,
// simulated directly here to decouple this test from exact byte-size
// arithmetic) -- channel B's cached configID now refers to a Filler that
// no longer exists. B's next write must detect that (rather than either
// erroring on a stale internal fcontainer.errStaleConfigID, or worse,
// silently colliding with an unrelated node that happens to reuse the same
// small integer id in the new Filler) and transparently re-add its config.
func TestSharedSegment_FlushInvalidatesOtherChannelsCachedConfig(t *testing.T) {
	rec := &fakeRecorder{}
	seg := newTestSegment(rec)
	pa := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	pb := NewCapturePolicy(2, seg, 1000, PolicyContinuous, PolicyParams{})

	pa.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	pb.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := pa.StartRecording(100, nil); err != nil {
		t.Fatalf("pa.StartRecording: %v", err)
	}
	if err := pb.StartRecording(100, nil); err != nil {
		t.Fatalf("pb.StartRecording: %v", err)
	}
	if err := pa.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("pa.HandleFrame: %v", err)
	}
	if err := pb.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindI)); err != nil {
		t.Fatalf("pb.HandleFrame: %v", err)
	}
	// Both channels now have a cached configEntry pointing into the current
	// Filler/generation.

	seg.mu.Lock()
	err := seg.flushLocked(150)
	seg.mu.Unlock()
	if err != nil {
		t.Fatalf("flushLocked: %v", err)
	}
	if len(rec.writes) != 1 || len(rec.writes[0].channels) != 2 {
		t.Fatalf("writes = %+v, want one write covering both channels", rec.writes)
	}

	// pa never itself detaches otherwise -- without this, channel 1 would
	// stay "active" forever and pb's own StopRecording below would never
	// see itself as the last one left, so it would never flush either.
	if err := pa.StopRecording(155); err != nil {
		t.Fatalf("pa.StopRecording: %v", err)
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want still 1 (channel 2 is still active)", len(rec.writes))
	}

	if err := pb.HandleFrame(0, fcontainer.KindVideo, vframe(160, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame for channel B after external flush: %v", err)
	}
	if err := pb.StopRecording(200); err != nil {
		t.Fatalf("pb.StopRecording: %v", err)
	}
	if len(rec.writes) != 2 {
		t.Fatalf("writes = %d, want 2 (channel B's post-flush frame written on its own stop)", len(rec.writes))
	}
	w := rec.writes[1]
	if len(w.channels) != 1 || w.channels[0] != 2 {
		t.Fatalf("second write channels = %v, want [2]", w.channels)
	}
	if countRole(w.filler, mediatree.RoleFrameVideo) != 1 {
		t.Fatalf("second write frame(video) count = %d, want 1", countRole(w.filler, mediatree.RoleFrameVideo))
	}
}
