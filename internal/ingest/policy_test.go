package ingest

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
)

// fakeUnderlyingSegment is a real *fcontainer.Filler wrapped to satisfy
// storage.Segment -- the fake counterpart of internal/storage's real
// segmentImpl, isolating internal/ingest's tests from real disk I/O
// exactly as the old fakeRecorder did for the pre-shared-segment design.
type fakeUnderlyingSegment struct {
	mu               sync.Mutex
	filler           *fcontainer.Filler
	closed           bool
	addStreamParamsN int
}

func newFakeUnderlyingSegment() *fakeUnderlyingSegment {
	return &fakeUnderlyingSegment{filler: fcontainer.New()}
}

func (f *fakeUnderlyingSegment) AddStreamParams(channel, stream uint32, kind fcontainer.StreamKind, params fcontainer.StreamParams) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, storage.ErrSegmentClosed
	}
	f.addStreamParamsN++
	return f.filler.AddStreamParams(channel, stream, kind, params)
}

func (f *fakeUnderlyingSegment) AddFrames(configID uint32, frames []fcontainer.Frame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return storage.ErrSegmentClosed
	}
	return f.filler.AddFrames(configID, frames)
}

func (f *fakeUnderlyingSegment) RegisterChannel(channel uint16) error { return nil }

func (f *fakeUnderlyingSegment) Elements() []mediatree.Element {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.filler.Elements()
}

func (f *fakeUnderlyingSegment) Close(now uint64) ([16]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return [16]byte{}, nil
}

// fakeSegmentBackend is a fake SegmentBackend: BeginSegment lazily creates
// (or returns the existing) fakeUnderlyingSegment, standing in for one
// Storage's real buffer pool always having at most one active segment.
type fakeSegmentBackend struct {
	mu         sync.Mutex
	current    *fakeUnderlyingSegment
	beginCount int
}

func (b *fakeSegmentBackend) BeginSegment(channels []uint16, now uint64) (storage.Segment, storage.PoolStatus, int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.beginCount++
	if b.current == nil {
		b.current = newFakeUnderlyingSegment()
	}
	return b.current, storage.PoolNormal, 0, nil
}

// rotate simulates a pool-driven fullness close: the current segment
// closes and the next call through StorageSegment transparently reopens a
// fresh one via BeginSegment, exactly like a real Segment rotating once
// its fblock fills up.
func (b *fakeSegmentBackend) rotate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current != nil {
		b.current.mu.Lock()
		b.current.closed = true
		b.current.mu.Unlock()
	}
	b.current = nil
}

// newTestSegment gives a test its own isolated shared segment -- one
// "Storage" of (by construction, unless another CapturePolicy is built
// over the same *StorageSegment) one channel, standing in for the old
// &fakeRecorder{} pattern.
func newTestSegment() (*StorageSegment, *fakeSegmentBackend) {
	b := &fakeSegmentBackend{}
	return newStorageSegment(b), b
}

func videoParams(t uint64) fcontainer.StreamParams {
	return fcontainer.StreamParams{Time: t, CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1, 2}, ParamPPS: []byte{3, 4}}
}

func vframe(t uint64, kind uint8) fcontainer.Frame {
	return fcontainer.Frame{Data: []byte("f"), Time: t, Kind: kind}
}

// frameTimesElems returns every decoded frame timestamp under timeRole, in
// tree (append) order -- this package's tests' replacement for asserting
// on a captured begin/end pair, since CapturePolicy no longer tracks those
// itself (Segment does, one layer down, in internal/storage).
func frameTimesElems(elems []mediatree.Element, timeRole mediatree.Role) []uint64 {
	var out []uint64
	for _, e := range elems {
		if e.Role == timeRole && len(e.Value) == 8 {
			out = append(out, binary.LittleEndian.Uint64(e.Value))
		}
	}
	return out
}

func TestContinuous_StartWithoutFromTimeDoesNotReplay(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
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

	elems := seg.Elements()
	times := frameTimesElems(elems, mediatree.RoleFrameTimeVideo)
	if len(times) != 1 || times[0] != 110 {
		t.Fatalf("frame times = %v, want [110] (frame at t=10 must not have replayed)", times)
	}
	if n := countRoleElems(elems, mediatree.RoleFrameVideo); n != 1 {
		t.Fatalf("frame(video) count = %d, want 1", n)
	}
}

func TestLiveSnapshot_GenerationBumpsOnceOnNewSegment(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if snap := p.LiveSnapshot(); snap.Recording || snap.Elements != nil {
		t.Fatalf("idle snapshot = %+v, want not recording, nil elements", snap)
	}

	if err := p.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	gen1 := p.LiveSnapshot().Generation

	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	snap := p.LiveSnapshot()
	if !snap.Recording {
		t.Fatal("expected Recording=true while a segment is open")
	}
	if snap.Generation != gen1 {
		t.Errorf("Generation changed within the same segment: %d -> %d", gen1, snap.Generation)
	}
	if len(snap.Elements) == 0 {
		t.Fatal("expected non-empty Elements after HandleFrame")
	}

	if err := p.StopRecording(200); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if err := p.StartRecording(300, nil); err != nil {
		t.Fatalf("second StartRecording: %v", err)
	}
	snap2 := p.LiveSnapshot()
	if snap2.Generation != gen1+1 {
		t.Errorf("Generation after new segment = %d, want %d", snap2.Generation, gen1+1)
	}
	// Content is no longer guaranteed empty here: the underlying fake
	// segment persists across this stop/start unless the pool actually
	// rotates it (fakeSegmentBackend.rotate() -- see
	// TestCapturePolicy_RotationMidRecording_RejoinsAndResetsConfigIDs in
	// policy_sharedsegment_test.go), matching the real design where
	// closing is purely fullness-driven, not tied to any one channel
	// stopping and restarting.
}

func TestOnRecordingChange_ReceivesStartAndStopTimes(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})

	type call struct {
		channel   uint16
		recording bool
		t         uint64
	}
	var calls []call
	p.SetOnRecordingChange(func(channel uint16, recording bool, t uint64) {
		calls = append(calls, call{channel, recording, t})
	})

	err := p.StartRecording(100, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	fromTime := uint64(50)
	err = p.StopRecording(200)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	err = p.StartRecording(300, &fromTime)
	if err != nil {
		t.Fatalf("StartRecording with fromTime: %v", err)
	}

	want := []call{
		{1, true, 100},  // StartRecording(100, nil): begin = now = 100
		{1, false, 200}, // StopRecording(200): end = now = 200
		{1, true, 50},   // StartRecording(300, &50): begin = fromTime = 50, not now
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Fatalf("calls[%d] = %+v, want %+v", i, c, want[i])
		}
	}
}

// TestOnRecordingChange_CanSafelyCallBackIntoPolicy guards the fix that
// moved onRecordingChange's invocation to after p.mu is released: the hook
// must be able to call any p.mu-guarded method (Policy here) without
// deadlocking on the same, non-reentrant mutex.
func TestOnRecordingChange_CanSafelyCallBackIntoPolicy(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})

	var gotType PolicyType
	p.SetOnRecordingChange(func(channel uint16, recording bool, t uint64) {
		gotType, _ = p.Policy()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.StartRecording(100, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartRecording did not return -- onRecordingChange likely still holds p.mu while firing")
	}
	if gotType != PolicyContinuous {
		t.Fatalf("onRecordingChange saw Policy() = %v, want PolicyContinuous", gotType)
	}
}

// TestOnRecordingChange_FiresEvenWhenReplayFails preserves the pre-refactor
// guarantee that the hook fires unconditionally on every actual p.recording
// flip, regardless of whether the subsequent replay of queued frames
// succeeds -- moving the call to after openSegmentLocked must not make it
// conditional on that call's error.
func TestOnRecordingChange_FiresEvenWhenReplayFails(t *testing.T) {
	seg, backend := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(10))
	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(10, 1)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}
	// Pre-seed the shared segment already closed (unlike rotate(), which
	// also nils backend.current so the next BeginSegment call would hand
	// back a fresh one) -- StorageSegment.call's one retry sees the same
	// closed segment both times and gives up with a hard error, making the
	// replay genuinely fail.
	backend.mu.Lock()
	backend.current = &fakeUnderlyingSegment{filler: fcontainer.New(), closed: true}
	backend.mu.Unlock()

	fired := false
	p.SetOnRecordingChange(func(channel uint16, recording bool, t uint64) {
		fired = true
	})

	err := p.StartRecording(10, nil) // replayFrom=10 matches the queued frame's own time, so it's actually replayed
	if err == nil {
		t.Fatal("StartRecording = nil error, want the replay failure to surface")
	}
	if !fired {
		t.Fatal("onRecordingChange did not fire despite the recording flip actually happening")
	}
}

func TestContinuous_StartWithFromTimeReplaysQueue(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
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

	elems := seg.Elements()
	times := frameTimesElems(elems, mediatree.RoleFrameTimeVideo)
	if len(times) != 2 || times[0] != 20 || times[1] != 30 {
		t.Fatalf("frame times = %v, want [20 30] (only frames >= 15 replayed)", times)
	}
	if got := countRoleElems(elems, mediatree.RoleFrameVideo); got != 2 {
		t.Fatalf("frame(video) count = %d, want 2", got)
	}
}

func TestContinuous_WrongCommandType(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	if err := p.Trigger(0, 0); !errors.Is(err, ErrWrongPolicyType) {
		t.Fatalf("Trigger on continuous = %v, want ErrWrongPolicyType", err)
	}
}

func TestEvent_TriggerInIdleReplaysPrerecordAndSetsStopAt(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyEvent, PolicyParams{Prerecord: 20, Postrecord: 50})
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

	times := frameTimesElems(seg.Elements(), mediatree.RoleFrameTimeVideo)
	if len(times) != 2 || times[0] != 80 || times[1] != 100 {
		t.Fatalf("frame times = %v, want [80 100]", times)
	}
}

func TestEvent_TriggerDuringRecordingExtendsButNeverShrinks(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyEvent, PolicyParams{Prerecord: 0, Postrecord: 50})
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
}

func TestEvent_WrongCommandType(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyEvent, PolicyParams{})
	if err := p.StartRecording(0, nil); !errors.Is(err, ErrWrongPolicyType) {
		t.Fatalf("StartRecording on event = %v, want ErrWrongPolicyType", err)
	}
}

func TestSetPolicy_OpenSegmentSurvivesSwapAndFallsUnderNewRules(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
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
	// The segment's content covers frames from both before and after the
	// swap.
	times := frameTimesElems(seg.Elements(), mediatree.RoleFrameTimeVideo)
	if len(times) == 0 || times[0] != 10 {
		t.Fatalf("frame times = %v, want first entry 10 (frame added before the swap)", times)
	}
}

func TestReplay_MultipleConfigVersionsAddedInOrder(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})

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

	elems := seg.Elements()
	if got := countRoleElems(elems, mediatree.RoleConfigVideo); got != 2 {
		t.Fatalf("config(video) node count = %d, want 2 (one per distinct params version)", got)
	}
	if got := countRoleElems(elems, mediatree.RoleFrameVideo); got != 2 {
		t.Fatalf("frame(video) count = %d, want 2", got)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCapturePolicy_Recording_ReflectsStartStop(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, uint64(time.Second), PolicyContinuous, PolicyParams{})

	if p.Recording() {
		t.Fatal("Recording() = true before StartRecording, want false")
	}

	if err := p.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if !p.Recording() {
		t.Fatal("Recording() = false after StartRecording, want true")
	}

	if err := p.StopRecording(1); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if p.Recording() {
		t.Fatal("Recording() = true after StopRecording, want false")
	}
}
