package ingest

import (
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/format"

	"github.com/traycers/farc/mediatree"
)

// TestChannelIngest_H264_BackpressureShedsWholeGOPsNotPartial is the direct
// regression test for .scratch/capture-keyframe-start/issues/
// 02-backpressure-gop-aware-shedding.md: the backpressure signal must only
// be re-evaluated at GOP boundaries (keyframes), so a GOP is always either
// fully recorded or fully dropped -- never split, even if the signal flips
// mid-GOP.
func TestChannelIngest_H264_BackpressureShedsWholeGOPsNotPartial(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	shedding := false
	ci.SetBackpressureSignal(func() bool { return shedding })

	pps := []byte{0x68, 0x04}
	src := &onPacketOnlySource{}
	f := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps352x288, PPS: pps}
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH264(src, nil, f, 0)

	idr := []byte{0x65, 0xaa}
	p := []byte{0x41, 0xbb}

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

	// GOP1: not shedding -- both frames recorded.
	send([][]byte{idr}, 0)
	send([][]byte{p}, 3000)

	// Flip mid-GOP2: signal is true when GOP2's I arrives (sheds the whole
	// GOP), then flips back to false before GOP2's P arrives -- the P must
	// still be dropped, since the decision already latched at the I.
	shedding = true
	send([][]byte{idr}, 6000)
	shedding = false
	send([][]byte{p}, 9000)

	// GOP3: not shedding by the time its I arrives -- both frames recorded,
	// and resumption starts cleanly at this GOP's own keyframe.
	send([][]byte{idr}, 12000)
	send([][]byte{p}, 15000)

	elems := seg.Elements()
	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 4 {
		t.Fatalf("frame(video) count = %d, want 4 (GOP1 x2 + GOP3 x2, GOP2 entirely dropped)", len(frameIDs))
	}
	kindOf := func(frameID uint32) uint8 {
		kindID, ok := mediatree.FindChildByRole(elems, frameID, mediatree.RoleFrameKind)
		if !ok || len(elems[kindID].Value) != 1 {
			t.Fatalf("frame %d: no frame_kind child", frameID)
		}
		return elems[kindID].Value[0]
	}
	wantKinds := []uint8{mediatree.FrameKindI, mediatree.FrameKindP, mediatree.FrameKindI, mediatree.FrameKindP}
	for i, id := range frameIDs {
		if got := kindOf(id); got != wantKinds[i] {
			t.Fatalf("frame %d kind = %v, want %v", i, got, wantKinds[i])
		}
	}
}

// TestChannelIngest_H265_BackpressureShedsWholeGOPsNotPartial mirrors the
// H264 test above for setupH265's symmetric gate.
func TestChannelIngest_H265_BackpressureShedsWholeGOPsNotPartial(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	shedding := false
	ci.SetBackpressureSignal(func() bool { return shedding })

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

	idr := []byte{0x26, 0x01} // NALUType_IDR_W_RADL = 19 -> (19<<1)=0x26
	p := []byte{0x00, 0x01}   // NALUType_TRAIL_N = 0

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

	send([][]byte{idr}, 0)
	send([][]byte{p}, 3000)

	shedding = true
	send([][]byte{idr}, 6000)
	shedding = false
	send([][]byte{p}, 9000)

	send([][]byte{idr}, 12000)
	send([][]byte{p}, 15000)

	elems := seg.Elements()
	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 4 {
		t.Fatalf("frame(video) count = %d, want 4 (GOP1 x2 + GOP3 x2, GOP2 entirely dropped)", len(frameIDs))
	}
	kindOf := func(frameID uint32) uint8 {
		kindID, ok := mediatree.FindChildByRole(elems, frameID, mediatree.RoleFrameKind)
		if !ok || len(elems[kindID].Value) != 1 {
			t.Fatalf("frame %d: no frame_kind child", frameID)
		}
		return elems[kindID].Value[0]
	}
	wantKinds := []uint8{mediatree.FrameKindI, mediatree.FrameKindP, mediatree.FrameKindI, mediatree.FrameKindP}
	for i, id := range frameIDs {
		if got := kindOf(id); got != wantKinds[i] {
			t.Fatalf("frame %d kind = %v, want %v", i, got, wantKinds[i])
		}
	}
}
