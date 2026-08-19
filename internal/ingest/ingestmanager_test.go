package ingest

import (
	"testing"
	"time"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// unreachableRTSPURL never actually connects (no listener at :1) --
// ChannelIngest.Run fails fast in the background and just logs, but the
// entry stays in IngestManager's map exactly as it would for a real camera
// that's merely unreachable right now. These tests only exercise
// IngestManager's own bookkeeping (map add/remove/list), not real streaming.
const unreachableRTSPURL = "rtsp://127.0.0.1:1/x"

func testChannelConfig(channel uint16, storageID string) ChannelConfig {
	return ChannelConfig{
		Channel:        channel,
		RTSPURL:        unreachableRTSPURL,
		StorageID:      storageID,
		SegmentBackend: &fakeSegmentBackend{},
		PolicyType:     PolicyContinuous,
		ReadTimeout:    time.Second,
		WriteTimeout:   time.Second,
	}
}

// testChannelConfigWithBackend is testChannelConfig but with a caller-owned
// SegmentBackend, so a test can drive/inspect a specific Storage's fake
// segment from outside (testChannelConfig's own &fakeSegmentBackend{} is
// unreachable from outside it).
func testChannelConfigWithBackend(channel uint16, storageID string, backend SegmentBackend) ChannelConfig {
	cfg := testChannelConfig(channel, storageID)
	cfg.SegmentBackend = backend
	return cfg
}

// policyOf reaches into IngestManager's private channel map to get at that
// channel's own CapturePolicy -- the only way to inject frames without a
// real RTSP source, since ChannelIngest.Run (fed by an actual RTSP session)
// is what normally does this.
func policyOf(t *testing.T, m *IngestManager, channel uint16) *CapturePolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.channels[channel]
	if !ok {
		t.Fatalf("policyOf: channel %d not found", channel)
	}
	return e.ingest.policy
}

func TestIngestManager_AddChannel_ThenListReportsIt(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	list := m.List()
	if len(list) != 1 || list[0].Channel != 1 || list[0].StorageID != "disk0" || list[0].RTSPURL != unreachableRTSPURL {
		t.Fatalf("List = %+v", list)
	}
}

func TestIngestManager_AddChannel_DuplicateRejected(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := m.AddChannel(testChannelConfig(1, "disk0")); err == nil {
		t.Fatalf("AddChannel: want error for duplicate channel id, got nil")
	}
}

func TestIngestManager_RemoveChannel_StopsItAndReturnsItsConfig(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	cfg, err := m.RemoveChannel(1)
	if err != nil {
		t.Fatalf("RemoveChannel: %v", err)
	}
	if cfg.Channel != 1 || cfg.StorageID != "disk0" {
		t.Fatalf("RemoveChannel returned cfg = %+v", cfg)
	}
	if len(m.List()) != 0 {
		t.Fatalf("List after remove = %+v, want empty", m.List())
	}

	// The channel id is free again -- Add should succeed, not conflict.
	err = m.AddChannel(testChannelConfig(1, "disk1"))
	if err != nil {
		t.Fatalf("AddChannel after remove: %v", err)
	}
}

func TestIngestManager_RemoveChannel_UnknownErrors(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	if _, err := m.RemoveChannel(99); err == nil {
		t.Fatalf("RemoveChannel: want error for unknown channel, got nil")
	}
}

func TestIngestManager_List_ReflectsLiveSetPolicyNotStaleConfig(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	err = m.SetPolicy(1, PolicyEvent, PolicyParams{Prerecord: 5, Postrecord: 10})
	if err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	list := m.List()
	if len(list) != 1 || list[0].PolicyType != PolicyEvent || list[0].PolicyParams != (PolicyParams{Prerecord: 5, Postrecord: 10}) {
		t.Fatalf("List after SetPolicy = %+v", list)
	}
}

func TestIngestManager_StartRecording_UnknownErrors(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	if err := m.StartRecording(99, 1, nil); err == nil {
		t.Fatalf("StartRecording: want error for unknown channel, got nil")
	}
}

func TestIngestManager_StartRecording_WrongPolicyTypeErrors(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	cfg := testChannelConfig(1, "disk0")
	cfg.PolicyType = PolicyEvent
	err := m.AddChannel(cfg)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	if err := m.StartRecording(1, 1, nil); err == nil {
		t.Fatalf("StartRecording: want ErrWrongPolicyType, got nil")
	}
}

func TestIngestManager_StopRecording_UnknownErrors(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	if err := m.StopRecording(99, 1); err == nil {
		t.Fatalf("StopRecording: want error for unknown channel, got nil")
	}
}

func TestIngestManager_StopRecording_WrongPolicyTypeErrors(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	cfg := testChannelConfig(1, "disk0")
	cfg.PolicyType = PolicyEvent
	err := m.AddChannel(cfg)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	if err := m.StopRecording(1, 1); err == nil {
		t.Fatalf("StopRecording: want ErrWrongPolicyType, got nil")
	}
}

func TestIngestManager_StartThenAddChannel_BothPresent(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	m.Start([]ChannelConfig{testChannelConfig(1, "disk0")})
	err := m.AddChannel(testChannelConfig(2, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("List = %+v, want 2 channels", list)
	}
}

func TestIngestManager_StorageOf(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	id, ok := m.StorageOf(1)
	if !ok || id != "disk0" {
		t.Fatalf("StorageOf(1) = %q, %v, want \"disk0\", true", id, ok)
	}

	_, ok = m.StorageOf(999)
	if ok {
		t.Fatal("StorageOf(999) unexpectedly found")
	}
}

// TestIngestManager_OnRecordingChange_CanSafelyCallList guards the exact
// scenario StorageOf originally existed to work around: CapturePolicy's
// onRecordingChange now fires after its own mutex is released
// (internal/ingest/policy.go), so a hook can safely call the full List()
// API -- which itself calls every channel's own policy.Policy(), including
// the very channel whose hook is firing -- without deadlocking on that
// channel's own, non-reentrant mutex. StorageOf (see its own doc comment)
// remains a valid, lighter-weight alternative, just no longer the only
// deadlock-safe option.
func TestIngestManager_OnRecordingChange_CanSafelyCallList(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	var got []ChannelInfo
	m.SetOnRecordingChange(func(channel uint16, recording bool, t uint64) {
		got = m.List()
	})
	err := m.AddChannel(testChannelConfig(1, "disk0"))
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.StartRecording(1, 1, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartRecording did not return -- onRecordingChange likely still holds CapturePolicy's own mutex while firing, deadlocking List's Policy() call")
	}
	if len(got) != 1 || got[0].StorageID != "disk0" {
		t.Fatalf("onRecordingChange saw List() = %+v, want exactly one channel on disk0", got)
	}
}

// TestIngestManager_StopRecording_DoesNotAffectOtherChannel is the
// end-to-end counterpart to CapturePolicy's own per-instance isolation:
// it drives two channels through IngestManager's actual dispatch path
// (StopRecording's map-lookup-then-call-that-channel's-CapturePolicy) and
// asserts that stopping channel 1 neither closes channel 2's open segment
// nor stops it from accepting further frames.
func TestIngestManager_StopRecording_DoesNotAffectOtherChannel(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	backend1, backend2 := &fakeSegmentBackend{}, &fakeSegmentBackend{}
	if err := m.AddChannel(testChannelConfigWithBackend(1, "disk0", backend1)); err != nil {
		t.Fatalf("AddChannel(1): %v", err)
	}
	if err := m.AddChannel(testChannelConfigWithBackend(2, "disk1", backend2)); err != nil {
		t.Fatalf("AddChannel(2): %v", err)
	}

	policyOf(t, m, 1).SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	policyOf(t, m, 2).SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if err := m.StartRecording(1, 100, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := m.StartRecording(2, 100, nil); err != nil {
		t.Fatalf("StartRecording(2): %v", err)
	}

	if err := policyOf(t, m, 1).HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(1): %v", err)
	}
	if err := policyOf(t, m, 2).HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(2): %v", err)
	}

	snap1, ok := m.LiveTree(1)
	if !ok || !snap1.Recording || len(snap1.Elements) == 0 {
		t.Fatalf("LiveTree(1) before stop = %+v, %v, want Recording=true, non-empty Elements", snap1, ok)
	}
	snap2, ok := m.LiveTree(2)
	if !ok || !snap2.Recording || len(snap2.Elements) == 0 {
		t.Fatalf("LiveTree(2) before stop = %+v, %v, want Recording=true, non-empty Elements", snap2, ok)
	}
	elemCount2 := len(snap2.Elements)

	if err := m.StopRecording(1, 200); err != nil {
		t.Fatalf("StopRecording(1): %v", err)
	}

	snap1, ok = m.LiveTree(1)
	if !ok || snap1.Recording {
		t.Fatalf("LiveTree(1) after stop = %+v, %v, want Recording=false", snap1, ok)
	}

	// The independence check: channel 2's own segment must be untouched by
	// channel 1's stop -- still recording, same element count. Channel 1's
	// stop is purely local to it (docs/docs/archive/00-requirements.md's
	// Close dynamics: closing is fullness-driven, not tied to any one
	// channel), so there's no "write" event to observe here at all --
	// unlike before this ticket, stopping a channel no longer finalizes
	// anything.
	snap2, ok = m.LiveTree(2)
	if !ok || !snap2.Recording || len(snap2.Elements) != elemCount2 {
		t.Fatalf("LiveTree(2) after channel 1's stop = %+v, %v, want unchanged Recording=true, %d elements", snap2, ok, elemCount2)
	}

	// Channel 2 must keep accepting frames after channel 1 stopped.
	if err := policyOf(t, m, 2).HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame(2) after channel 1's stop: %v", err)
	}
	snap2, ok = m.LiveTree(2)
	if !ok || len(snap2.Elements) <= elemCount2 {
		t.Fatalf("LiveTree(2) after extra frame = %+v, %v, want more than %d elements", snap2, ok, elemCount2)
	}

	if err := m.StopRecording(2, 300); err != nil {
		t.Fatalf("StopRecording(2): %v", err)
	}
}

// TestIngestManager_RemoveChannel_DoesNotAffectOtherChannel checks the
// same independence property for the more drastic RemoveChannel path
// (cancel + wait for that channel's own goroutine) rather than a plain
// StopRecording call.
func TestIngestManager_RemoveChannel_DoesNotAffectOtherChannel(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	backend1, backend2 := &fakeSegmentBackend{}, &fakeSegmentBackend{}
	if err := m.AddChannel(testChannelConfigWithBackend(1, "disk0", backend1)); err != nil {
		t.Fatalf("AddChannel(1): %v", err)
	}
	if err := m.AddChannel(testChannelConfigWithBackend(2, "disk1", backend2)); err != nil {
		t.Fatalf("AddChannel(2): %v", err)
	}

	policyOf(t, m, 1).SetStreamParams(0, fcontainer.KindVideo, videoParams(0))
	policyOf(t, m, 2).SetStreamParams(0, fcontainer.KindVideo, videoParams(0))

	if err := m.StartRecording(1, 100, nil); err != nil {
		t.Fatalf("StartRecording(1): %v", err)
	}
	if err := m.StartRecording(2, 100, nil); err != nil {
		t.Fatalf("StartRecording(2): %v", err)
	}
	if err := policyOf(t, m, 1).HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(1): %v", err)
	}
	if err := policyOf(t, m, 2).HandleFrame(0, fcontainer.KindVideo, vframe(110, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame(2): %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := m.RemoveChannel(1); err != nil {
			t.Errorf("RemoveChannel(1): %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveChannel(1) did not return -- likely blocked on channel 2's state")
	}

	list := m.List()
	if len(list) != 1 || list[0].Channel != 2 {
		t.Fatalf("List after RemoveChannel(1) = %+v, want only channel 2", list)
	}

	snap2, ok := m.LiveTree(2)
	if !ok || !snap2.Recording || len(snap2.Elements) == 0 {
		t.Fatalf("LiveTree(2) after RemoveChannel(1) = %+v, %v, want unchanged Recording=true, non-empty Elements", snap2, ok)
	}
	elemCount2 := len(snap2.Elements)

	// Channel 2 must keep accepting frames after channel 1 was removed.
	if err := policyOf(t, m, 2).HandleFrame(0, fcontainer.KindVideo, vframe(120, mediatree.FrameKindP)); err != nil {
		t.Fatalf("HandleFrame(2) after RemoveChannel(1): %v", err)
	}
	snap2, ok = m.LiveTree(2)
	if !ok || len(snap2.Elements) <= elemCount2 {
		t.Fatalf("LiveTree(2) after extra frame = %+v, %v, want more than %d elements", snap2, ok, elemCount2)
	}
}

// TestIngestManager_ReplaceChannel_SwapsConfigAndReturnsOld covers the happy
// path: ReplaceChannel(channel, newCfg) removes channel's current config,
// starts it with newCfg, and returns the config it replaced.
func TestIngestManager_ReplaceChannel_SwapsConfigAndReturnsOld(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	oldCfg := testChannelConfig(1, "disk0")
	if err := m.AddChannel(oldCfg); err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	newCfg := testChannelConfig(1, "disk1")
	got, err := m.ReplaceChannel(1, newCfg)
	if err != nil {
		t.Fatalf("ReplaceChannel: %v", err)
	}
	if got.StorageID != "disk0" {
		t.Fatalf("ReplaceChannel returned old = %+v, want StorageID disk0", got)
	}

	list := m.List()
	if len(list) != 1 || list[0].StorageID != "disk1" {
		t.Fatalf("List() after ReplaceChannel = %+v, want one channel on disk1", list)
	}
}

// TestIngestManager_ReplaceChannel_RestoresOldOnAddFailure exercises
// ReplaceChannel's rollback guarantee directly, by forcing the internal
// AddChannel(newCfg) step to fail (newCfg claims a channel id that's
// already running elsewhere) rather than relying on a real concurrent
// race -- the same failure mode handleUpdateChannel currently reacts to.
func TestIngestManager_ReplaceChannel_RestoresOldOnAddFailure(t *testing.T) {
	m := NewIngestManager()
	defer m.Stop()

	if err := m.AddChannel(testChannelConfig(1, "disk0")); err != nil {
		t.Fatalf("AddChannel(1): %v", err)
	}
	if err := m.AddChannel(testChannelConfig(2, "disk2")); err != nil {
		t.Fatalf("AddChannel(2): %v", err)
	}

	conflictingCfg := testChannelConfig(2, "disk9") // id 2 is already running
	_, err := m.ReplaceChannel(1, conflictingCfg)
	if err == nil {
		t.Fatal("ReplaceChannel = nil error, want the AddChannel(newCfg) conflict to surface")
	}

	found := false
	for _, ci := range m.List() {
		if ci.Channel == 1 && ci.StorageID == "disk0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("List() after failed ReplaceChannel = %+v, want channel 1 restored to disk0", m.List())
	}
}
