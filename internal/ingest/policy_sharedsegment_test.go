package ingest

import (
	"sync"
	"testing"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

func TestCapturePolicy_TwoPolicies_SameStorage_ShareSegment(t *testing.T) {
	seg, backend := newTestSegment()
	p1 := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p2 := NewCapturePolicy(2, seg, 1000, PolicyContinuous, PolicyParams{})

	p1.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	p2.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if err := p1.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := p2.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording(2): %v", err)
	}
	if err := p1.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(1): %v", err)
	}
	if err := p2.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(2): %v", err)
	}

	if backend.beginCount != 1 {
		t.Fatalf("BeginSegment called %d times, want 1 (both channels share one segment)", backend.beginCount)
	}
	elems := seg.Elements()
	if !hasChannelNode(elems, 1) || !hasChannelNode(elems, 2) {
		t.Fatal("expected both channel 1 and channel 2 subtrees in the shared segment")
	}
}

func TestCapturePolicy_OneChannelStopping_DoesNotAffectOtherChannelsSegment(t *testing.T) {
	seg, _ := newTestSegment()
	p1 := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p2 := NewCapturePolicy(2, seg, 1000, PolicyContinuous, PolicyParams{})

	p1.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	p2.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := p1.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := p2.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording(2): %v", err)
	}
	if err := p2.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(2) before stop: %v", err)
	}
	genBefore := seg.Generation()

	if err := p1.StopRecording(200); err != nil {
		t.Fatalf("StopRecording(1): %v", err)
	}

	if seg.Generation() != genBefore {
		t.Fatalf("Generation() changed from %d to %d just because channel 1 stopped -- closing must be purely fullness-driven", genBefore, seg.Generation())
	}
	if err := p2.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame(2) after channel 1's stop: %v", err)
	}
	snap2 := p2.LiveSnapshot()
	if !snap2.Recording {
		t.Fatal("channel 2 should still be recording after channel 1 stopped")
	}
	times := frameTimesElems(snap2.Elements, mediatree.RoleFrameTimeVideo)
	if len(times) != 2 || times[0] != 110 || times[1] != 120 {
		t.Fatalf("channel 2 frame times = %v, want [110 120] (unaffected by channel 1's stop)", times)
	}
}

func TestCapturePolicy_MixedContinuousAndEventPolicies_ShareSegment(t *testing.T) {
	seg, backend := newTestSegment()
	p1 := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p2 := NewCapturePolicy(2, seg, 1000, PolicyEvent, PolicyParams{Prerecord: 0, Postrecord: 50})

	p1.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	p2.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if err := p1.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := p1.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(1): %v", err)
	}

	// Channel 2 (event policy) triggers mid-way through channel 1's
	// continuous recording.
	if err := p2.Trigger(150, 150); err != nil {
		t.Fatalf("Trigger(2): %v", err)
	}
	if err := p2.HandleFrame(0, fcontainer.KindVideo, vframe(160, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(2): %v", err)
	}

	if backend.beginCount != 1 {
		t.Fatalf("BeginSegment called %d times, want 1 (mixed policy types still share one segment)", backend.beginCount)
	}
	elems := seg.Elements()
	if !hasChannelNode(elems, 1) || !hasChannelNode(elems, 2) {
		t.Fatal("expected both channels present despite different policy types")
	}
}

func TestCapturePolicy_RotationMidRecording_RejoinsAndResetsConfigIDs(t *testing.T) {
	seg, backend := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if err := p.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	firstUnderlying := backend.current
	if firstUnderlying.addStreamParamsN != 1 {
		t.Fatalf("addStreamParamsN on first segment = %d, want 1", firstUnderlying.addStreamParamsN)
	}
	genBefore := p.LiveSnapshot().Generation

	backend.rotate() // pool-driven fullness close, simulated

	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame after rotation: %v", err)
	}

	if backend.beginCount != 2 {
		t.Fatalf("BeginSegment called %d times, want 2 (initial + reopen after rotation)", backend.beginCount)
	}
	genAfter := p.LiveSnapshot().Generation
	if genAfter != genBefore+1 {
		t.Fatalf("Generation = %d, want %d (exactly one bump for the rotation)", genAfter, genBefore+1)
	}
	newUnderlying := backend.current
	if newUnderlying == firstUnderlying {
		t.Fatal("expected a distinct underlying segment after rotation")
	}
	if newUnderlying.addStreamParamsN != 1 {
		t.Fatalf("addStreamParamsN on the new segment = %d, want 1 (config re-added transparently after rotation)", newUnderlying.addStreamParamsN)
	}
}

func TestCapturePolicy_ConcurrentHandleFrame_TwoChannels_RaceDetectorClean(t *testing.T) {
	seg, _ := newTestSegment()
	p1 := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p2 := NewCapturePolicy(2, seg, 1000, PolicyContinuous, PolicyParams{})
	p1.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	p2.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	if err := p1.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := p2.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording(2): %v", err)
	}

	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	drive := func(p *CapturePolicy, base uint64) {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(base+uint64(i), mediatree.FrameKindP)); err != nil {
				t.Errorf("HandleFrame: %v", err)
			}
		}
	}
	go drive(p1, 1_000_000)
	go drive(p2, 2_000_000)
	wg.Wait()

	merged := seg.Elements()
	got1 := countRoleElems(filterChannelElements(merged, 1), mediatree.RoleFrameVideo)
	got2 := countRoleElems(filterChannelElements(merged, 2), mediatree.RoleFrameVideo)
	if got1 != n {
		t.Errorf("channel 1 frame count = %d, want %d", got1, n)
	}
	if got2 != n {
		t.Errorf("channel 2 frame count = %d, want %d", got2, n)
	}
}
