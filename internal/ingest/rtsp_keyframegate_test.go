package ingest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"

	"github.com/traycers/farc/mediatree"
)

// TestChannelIngest_H264_DropsLeadingNonKeyframeFrames is the direct
// regression test for .scratch/capture-keyframe-start/issues/
// 01-first-frame-not-guaranteed-keyframe.md: a stream whose first decoded
// access units are P-frames (no reference I-frame available yet) must not
// have those frames recorded -- only frames from the first keyframe onward
// belong in the fcontainer.
func TestChannelIngest_H264_DropsLeadingNonKeyframeFrames(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	pps := []byte{0x68, 0x04}
	src := &onPacketOnlySource{}
	// SPS/PPS advertised at the SDP level (unlike rtsp_paramscompare_test.go's
	// in-band-only camera) so stream params are valid from the start --
	// otherwise a leading frame would fail fcontainer's "missing param_sps"
	// validation regardless of the keyframe gate under test here, giving a
	// false-positive pass.
	f := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps352x288, PPS: pps}
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH264(src, nil, f, 0)

	idr := []byte{0x65, 0xaa, 0xaa}
	p1 := []byte{0x41, 0xbb}
	p2 := []byte{0x41, 0xcc}
	p3 := []byte{0x41, 0xdd}

	send := func(nalus [][]byte, ts uint32) {
		packets, err := rtpEnc.Encode(nalus)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for _, pkt := range packets {
			pkt.Timestamp = ts
			src.cb(pkt)
		}
	}

	// Two leading P-frames arrive before any keyframe -- e.g. the camera's
	// jitter buffer/RTP session delivered mid-GOP media first.
	send([][]byte{p1}, 0)
	send([][]byte{p2}, 3000)
	send([][]byte{idr}, 6000)
	send([][]byte{p3}, 9000)

	elems := seg.Elements()
	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 2 {
		t.Fatalf("frame(video) count = %d, want 2 (leading P-frames must be dropped)", len(frameIDs))
	}
	kindOf := func(frameID uint32) uint8 {
		kindID, ok := mediatree.FindChildByRole(elems, frameID, mediatree.RoleFrameKind)
		if !ok || len(elems[kindID].Value) != 1 {
			t.Fatalf("frame %d: no frame_kind child", frameID)
		}
		return elems[kindID].Value[0]
	}
	if kindOf(frameIDs[0]) != mediatree.FrameKindI {
		t.Fatalf("first recorded frame kind = %v, want I", kindOf(frameIDs[0]))
	}
	if kindOf(frameIDs[1]) != mediatree.FrameKindP {
		t.Fatalf("second recorded frame kind = %v, want P", kindOf(frameIDs[1]))
	}
}

// TestChannelIngest_H264_LogsCountOfDroppedLeadingFrames covers the normal
// (non-pathological) case's observability requirement: once the first
// keyframe arrives, log how many leading non-keyframe frames were dropped
// while waiting for it.
func TestChannelIngest_H264_LogsCountOfDroppedLeadingFrames(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	var lines []string
	ci.SetLogger(func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) })

	pps := []byte{0x68, 0x04}
	src := &onPacketOnlySource{}
	f := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps352x288, PPS: pps}
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH264(src, nil, f, 0)

	idr := []byte{0x65, 0xaa, 0xaa}
	p1 := []byte{0x41, 0xbb}
	p2 := []byte{0x41, 0xcc}

	send := func(nalus [][]byte, ts uint32) {
		packets, err := rtpEnc.Encode(nalus)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for _, pkt := range packets {
			pkt.Timestamp = ts
			src.cb(pkt)
		}
	}

	send([][]byte{p1}, 0)
	send([][]byte{p2}, 3000)
	send([][]byte{idr}, 6000)

	found := false
	for _, l := range lines {
		if strings.Contains(l, "dropped 2 leading non-keyframe") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a log line reporting 2 dropped leading frames, got: %v", lines)
	}
}

// TestChannelIngest_H264_WarnsIfNoKeyframeArrivesWithinTimeout covers the
// pathological case: a camera that never sends a keyframe at all must not
// silently wait forever with no observable signal.
func TestChannelIngest_H264_WarnsIfNoKeyframeArrivesWithinTimeout(t *testing.T) {
	orig := keyframeWaitTimeout
	keyframeWaitTimeout = 10 * time.Millisecond
	defer func() { keyframeWaitTimeout = orig }()

	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	var mu sync.Mutex
	var lines []string
	ci.SetLogger(func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	})

	pps := []byte{0x68, 0x04}
	src := &onPacketOnlySource{}
	f := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps352x288, PPS: pps}
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH264(src, nil, f, 0)

	// Only ever P-frames -- the pathological "no I-frame ever" case.
	p1 := []byte{0x41, 0xbb}
	packets, err := rtpEnc.Encode([][]byte{p1})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, pkt := range packets {
		src.cb(pkt)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := false
		for _, l := range lines {
			if strings.Contains(l, "no keyframe") {
				found = true
			}
		}
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected a warning log line about no keyframe arriving, got: %v", lines)
}

// TestChannelIngest_H265_DropsLeadingNonKeyframeFrames mirrors the H264
// test above for setupH265's symmetric gate.
func TestChannelIngest_H265_DropsLeadingNonKeyframeFrames(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	vps := []byte{0x40, 0x01}
	sps := []byte{0x42, 0x01}
	pps := []byte{0x44, 0x01}
	src := &onPacketOnlySource{}
	f := &format.H265{PayloadTyp: 96, VPS: vps, SPS: sps, PPS: pps}
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH265(src, nil, f, 0)

	idr := []byte{0x26, 0x01, 0xaa} // NALUType_IDR_W_RADL = 19 -> (19<<1)=0x26
	p1 := []byte{0x00, 0x01, 0xbb}  // NALUType_TRAIL_N = 0
	p2 := []byte{0x00, 0x01, 0xcc}
	p3 := []byte{0x00, 0x01, 0xdd}

	send := func(nalus [][]byte, ts uint32) {
		packets, err := rtpEnc.Encode(nalus)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for _, pkt := range packets {
			pkt.Timestamp = ts
			src.cb(pkt)
		}
	}

	send([][]byte{p1}, 0)
	send([][]byte{p2}, 3000)
	send([][]byte{idr}, 6000)
	send([][]byte{p3}, 9000)

	elems := seg.Elements()
	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 2 {
		t.Fatalf("frame(video) count = %d, want 2 (leading P-frames must be dropped)", len(frameIDs))
	}
	kindOf := func(frameID uint32) uint8 {
		kindID, ok := mediatree.FindChildByRole(elems, frameID, mediatree.RoleFrameKind)
		if !ok || len(elems[kindID].Value) != 1 {
			t.Fatalf("frame %d: no frame_kind child", frameID)
		}
		return elems[kindID].Value[0]
	}
	if kindOf(frameIDs[0]) != mediatree.FrameKindI {
		t.Fatalf("first recorded frame kind = %v, want I", kindOf(frameIDs[0]))
	}
	if kindOf(frameIDs[1]) != mediatree.FrameKindP {
		t.Fatalf("second recorded frame kind = %v, want P", kindOf(frameIDs[1]))
	}
}
