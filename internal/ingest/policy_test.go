package ingest

import (
	"errors"
	"testing"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

type recordedWrite struct {
	channels []uint16
	begin    uint64
	end      uint64
	filler   *fcontainer.Filler
}

type fakeRecorder struct {
	writes []recordedWrite
}

func (r *fakeRecorder) WriteFcontainer(channels []uint16, begin, end uint64, filler *fcontainer.Filler, now uint64) ([16]byte, error) {
	r.writes = append(r.writes, recordedWrite{append([]uint16(nil), channels...), begin, end, filler})
	return [16]byte{byte(len(r.writes))}, nil
}

func videoParams(t uint64) fcontainer.StreamParams {
	return fcontainer.StreamParams{Time: t, CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1, 2}, ParamPPS: []byte{3, 4}}
}

func vframe(t uint64, kind uint8) fcontainer.Frame {
	return fcontainer.Frame{Data: []byte("f"), Time: t, Kind: kind}
}

func countRole(f *fcontainer.Filler, role mediatree.Role) int {
	n := 0
	for _, e := range f.Elements() {
		if e.Role == role {
			n++
		}
	}
	return n
}

func TestContinuous_StartWithoutFromTimeDoesNotReplay(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	err := p.HandleFrame(0, fcontainer.KindVideo, vframe(10, mediatree.FrameKindI))
	if err != nil {
		t.Fatalf("HandleFrame (idle, queued only): %v", err)
	}

	err = p.StartRecording(100, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	err = p.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindP))
	if err != nil {
		t.Fatalf("HandleFrame (recording): %v", err)
	}
	err = p.StopRecording(200)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}

	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	w := rec.writes[0]
	if w.begin != 110 || w.end != 110 {
		t.Fatalf("begin/end = %d/%d, want 110/110 (frame at t=10 must not have replayed)", w.begin, w.end)
	}
	if countRole(w.filler, mediatree.RoleFrameVideo) != 1 {
		t.Fatalf("frame(video) count = %d, want 1", countRole(w.filler, mediatree.RoleFrameVideo))
	}
}

func TestContinuous_StartWithFromTimeReplaysQueue(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	for _, ts := range []uint64{10, 20, 30} {
		err := p.HandleFrame(0, fcontainer.KindVideo, vframe(ts, mediatree.FrameKindI))
		if err != nil {
			t.Fatalf("HandleFrame: %v", err)
		}
	}

	err := p.StartRecording(100, ptr(uint64(15)))
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	err = p.StopRecording(200)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}

	w := rec.writes[0]
	if w.begin != 20 || w.end != 30 {
		t.Fatalf("begin/end = %d/%d, want 20/30 (only frames >= 15 replayed)", w.begin, w.end)
	}
	if got := countRole(w.filler, mediatree.RoleFrameVideo); got != 2 {
		t.Fatalf("frame(video) count = %d, want 2", got)
	}
}

func TestContinuous_WrongCommandType(t *testing.T) {
	p := NewCapturePolicy(1, &fakeRecorder{}, 1000, PolicyContinuous, PolicyParams{})
	if err := p.Trigger(0, 0); !errors.Is(err, ErrWrongPolicyType) {
		t.Fatalf("Trigger on continuous = %v, want ErrWrongPolicyType", err)
	}
}

func TestEvent_TriggerInIdleReplaysPrerecordAndSetsStopAt(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyEvent, PolicyParams{Prerecord: 20, Postrecord: 50})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	for _, ts := range []uint64{60, 80, 100} {
		err := p.HandleFrame(0, fcontainer.KindVideo, vframe(ts, mediatree.FrameKindI))
		if err != nil {
			t.Fatalf("HandleFrame: %v", err)
		}
	}

	// event at t=100: prerecord window [80,100] -> replays ts=80,100.
	err := p.Trigger(100, 100)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !p.recording {
		t.Fatal("should be recording after Trigger in idle")
	}
	if p.stopAt != 150 {
		t.Fatalf("stopAt = %d, want 150 (100+50)", p.stopAt)
	}

	err = p.Tick(149)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !p.recording {
		t.Fatal("should still be recording before stop_at")
	}

	err = p.Tick(150)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if p.recording {
		t.Fatal("should have closed at stop_at")
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	if w := rec.writes[0]; w.begin != 80 || w.end != 100 {
		t.Fatalf("begin/end = %d/%d, want 80/100", w.begin, w.end)
	}
}

func TestEvent_TriggerDuringRecordingExtendsButNeverShrinks(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyEvent, PolicyParams{Prerecord: 0, Postrecord: 50})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	err := p.Trigger(100, 100)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if p.stopAt != 150 {
		t.Fatalf("stopAt = %d, want 150", p.stopAt)
	}

	// A later trigger with a bigger stop_at candidate extends.
	err = p.Trigger(120, 120)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if p.stopAt != 170 {
		t.Fatalf("stopAt = %d, want 170 (extended)", p.stopAt)
	}

	// A trigger whose candidate is smaller must NOT shrink stop_at.
	err = p.Trigger(130, 90)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if p.stopAt != 170 {
		t.Fatalf("stopAt = %d, want 170 (must not shrink)", p.stopAt)
	}

	if len(rec.writes) != 0 {
		t.Fatalf("writes = %d, want 0 (still recording)", len(rec.writes))
	}
}

func TestEvent_WrongCommandType(t *testing.T) {
	p := NewCapturePolicy(1, &fakeRecorder{}, 1000, PolicyEvent, PolicyParams{})
	if err := p.StartRecording(0, nil); !errors.Is(err, ErrWrongPolicyType) {
		t.Fatalf("StartRecording on event = %v, want ErrWrongPolicyType", err)
	}
}

func TestSetPolicy_OpenSegmentSurvivesSwapAndFallsUnderNewRules(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	err := p.StartRecording(0, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	err = p.HandleFrame(0, fcontainer.KindVideo, vframe(10, mediatree.FrameKindI))
	if err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}

	// Swap to event mid-segment: segment must survive, stop_at stays unset
	// until a real Trigger (§6).
	p.SetPolicy(PolicyEvent, PolicyParams{Prerecord: 0, Postrecord: 30})
	if !p.recording {
		t.Fatal("segment should survive a SetPolicy swap")
	}
	if p.stopAtSet {
		t.Fatal("stop_at must not be assigned artificially on swap")
	}

	// A tick must not close it (no stop_at set yet).
	err = p.Tick(1_000_000)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !p.recording {
		t.Fatal("should still be open: stop_at never set")
	}

	// First real trigger under the new policy sets stop_at via plain
	// assignment (max degenerates since there was no prior value).
	err = p.Trigger(20, 20)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if p.stopAt != 50 {
		t.Fatalf("stopAt = %d, want 50", p.stopAt)
	}
	err = p.Tick(50)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if p.recording {
		t.Fatal("should have closed")
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	// The single write covers frames from both before and after the swap.
	if w := rec.writes[0]; w.begin != 10 {
		t.Fatalf("begin = %d, want 10 (frame added before the swap)", w.begin)
	}
}

func TestReplay_MultipleConfigVersionsAddedInOrder(t *testing.T) {
	rec := &fakeRecorder{}
	p := NewCapturePolicy(1, rec, 1000, PolicyContinuous, PolicyParams{})

	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	err := p.HandleFrame(0, fcontainer.KindVideo, vframe(10, mediatree.FrameKindI))
	if err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	// Params change mid-queue (new SPS/PPS) -> a distinct config version.
	newParams := fcontainer.StreamParams{Time: 15, CodecVideo: mediatree.CodecH264, ParamSPS: []byte{9, 9}, ParamPPS: []byte{8, 8}}
	p.SetStreamParams(0, fcontainer.KindVideo, newParams)
	err = p.HandleFrame(0, fcontainer.KindVideo, vframe(20, mediatree.FrameKindI))
	if err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}

	err = p.StartRecording(100, ptr(uint64(0)))
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	err = p.StopRecording(200)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}

	w := rec.writes[0]
	if got := countRole(w.filler, mediatree.RoleConfigVideo); got != 2 {
		t.Fatalf("config(video) node count = %d, want 2 (one per distinct params version)", got)
	}
	if got := countRole(w.filler, mediatree.RoleFrameVideo); got != 2 {
		t.Fatalf("frame(video) count = %d, want 2", got)
	}
}

func ptr[T any](v T) *T { return &v }
