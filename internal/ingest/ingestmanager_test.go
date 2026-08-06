package ingest

import (
	"testing"
	"time"
)

// unreachableRTSPURL never actually connects (no listener at :1) --
// ChannelIngest.Run fails fast in the background and just logs, but the
// entry stays in IngestManager's map exactly as it would for a real camera
// that's merely unreachable right now. These tests only exercise
// IngestManager's own bookkeeping (map add/remove/list), not real streaming.
const unreachableRTSPURL = "rtsp://127.0.0.1:1/x"

func testChannelConfig(channel uint16, storageID string) ChannelConfig {
	return ChannelConfig{
		Channel:      channel,
		RTSPURL:      unreachableRTSPURL,
		StorageID:    storageID,
		Recorder:     &fakeRecorder{},
		PolicyType:   PolicyContinuous,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	}
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
