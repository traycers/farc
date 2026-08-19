package ingest

import (
	"encoding/binary"
	"testing"

	"github.com/bluenviron/gortsplib/v4/pkg/format"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// sps352x288/sps1280x720 are real H.264 SPS NALUs (mediacommon's own
// h264.SPS test fixtures) -- used here so buildVideoParams/videoParamsChanged
// are exercised against genuinely parseable resolutions, not just NALU-type
// placeholder bytes.
var (
	sps352x288 = []byte{
		0x67, 0x64, 0x00, 0x0c, 0xac, 0x3b, 0x50, 0xb0,
		0x4b, 0x42, 0x00, 0x00, 0x03, 0x00, 0x02, 0x00,
		0x00, 0x03, 0x00, 0x3d, 0x08,
	}
	sps1280x720 = []byte{
		0x67, 0x64, 0x00, 0x1f, 0xac, 0xd9, 0x40, 0x50,
		0x05, 0xbb, 0x01, 0x6c, 0x80, 0x00, 0x00, 0x03,
		0x00, 0x80, 0x00, 0x00, 0x1e, 0x07, 0x8c, 0x18,
		0xcb,
	}
)

func TestBuildVideoParams_ParsesResolutionFromRealSPS(t *testing.T) {
	p := buildVideoParams(mediatree.CodecH264, nil, sps352x288, []byte{0x68, 1})
	if p.Width != 352 || p.Height != 288 {
		t.Fatalf("Width/Height = %d/%d, want 352/288", p.Width, p.Height)
	}

	p = buildVideoParams(mediatree.CodecH264, nil, sps1280x720, []byte{0x68, 1})
	if p.Width != 1280 || p.Height != 720 {
		t.Fatalf("Width/Height = %d/%d, want 1280/720", p.Width, p.Height)
	}
}

func TestBuildVideoParams_UnparseableSPSLeavesResolutionZero(t *testing.T) {
	// A NALU-type-only placeholder, like real cameras' in-band NALUs are
	// often synthesized in tests elsewhere in this package -- not a real
	// SPS bitstream, so mediacommon's Unmarshal must fail cleanly.
	p := buildVideoParams(mediatree.CodecH264, nil, []byte{0x67, 1, 2, 3}, []byte{0x68, 4})
	if p.Width != 0 || p.Height != 0 {
		t.Fatalf("Width/Height = %d/%d, want 0/0 for an unparseable SPS", p.Width, p.Height)
	}
}

func TestVideoParamsChanged(t *testing.T) {
	sameRes720 := buildVideoParams(mediatree.CodecH264, nil, sps1280x720, []byte{0x68, 1})
	sameRes720Again := buildVideoParams(mediatree.CodecH264, nil, sps1280x720, []byte{0x68, 1})
	res288 := buildVideoParams(mediatree.CodecH264, nil, sps352x288, []byte{0x68, 1})
	h265Codec := sameRes720
	h265Codec.CodecVideo = mediatree.CodecH265

	unparseableA := buildVideoParams(mediatree.CodecH264, nil, []byte{0x67, 1, 2}, []byte{0x68, 3})
	unparseableASame := buildVideoParams(mediatree.CodecH264, nil, []byte{0x67, 1, 2}, []byte{0x68, 3})
	unparseableB := buildVideoParams(mediatree.CodecH264, nil, []byte{0x67, 9, 9}, []byte{0x68, 3})

	cases := []struct {
		name string
		old  fcontainer.StreamParams
		next fcontainer.StreamParams
		want bool
	}{
		{"identical real resolution", sameRes720, sameRes720Again, false},
		{"different real resolution", sameRes720, res288, true},
		{"different codec", sameRes720, h265Codec, true},
		{"unparseable both sides, identical bytes -> byte-compare fallback, unchanged", unparseableA, unparseableASame, false},
		{"unparseable both sides, different bytes -> byte-compare fallback, changed", unparseableA, unparseableB, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := videoParamsChanged(c.old, c.next); got != c.want {
				t.Fatalf("videoParamsChanged = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAudioParamsChanged(t *testing.T) {
	base := fcontainer.StreamParams{CodecAudio: mediatree.CodecAAC, SampleRate: 48000, ChannelCount: 2}
	cases := []struct {
		name string
		next fcontainer.StreamParams
		want bool
	}{
		{"identical", base, false},
		{"different sample rate", fcontainer.StreamParams{CodecAudio: mediatree.CodecAAC, SampleRate: 16000, ChannelCount: 2}, true},
		{"different channel count", fcontainer.StreamParams{CodecAudio: mediatree.CodecAAC, SampleRate: 48000, ChannelCount: 1}, true},
		{"different codec", fcontainer.StreamParams{CodecAudio: mediatree.CodecG711A, SampleRate: 48000, ChannelCount: 2}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := audioParamsChanged(base, c.next); got != c.want {
				t.Fatalf("audioParamsChanged = %v, want %v", got, c.want)
			}
		})
	}
}

// TestChannelIngest_H264_RepeatedIdenticalInBandSPSPPS_NoNewConfig is the
// direct regression test for the reported bug: a camera that never
// advertises sprop-parameter-sets in SDP, only sending SPS/PPS in-band
// before every IDR (common real-world behavior), must still end up with
// exactly one config(video) node, not one per keyframe.
func TestChannelIngest_H264_RepeatedIdenticalInBandSPSPPS_NoNewConfig(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	src := &onPacketOnlySource{}
	f := &format.H264{PayloadTyp: 96, PacketizationMode: 1} // no SDP-level SPS/PPS
	rtpEnc, err := f.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	ci.setupH264(src, nil, f, 0)

	sps := []byte{0x67, 0x01, 0x02, 0x03}
	pps := []byte{0x68, 0x04}
	idr := []byte{0x65, 0xaa, 0xaa}

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

	// Three "keyframes", each resending byte-identical SPS/PPS in-band --
	// exactly the pattern that used to spuriously reopen a new config node
	// on every one of them.
	send([][]byte{sps, pps, idr}, 0)
	send([][]byte{sps, pps, idr}, 3000)
	send([][]byte{sps, pps, idr}, 6000)

	elems := seg.Elements()
	if got := countRoleElems(elems, mediatree.RoleConfigVideo); got != 1 {
		t.Fatalf("config(video) node count = %d, want 1 (repeated identical in-band SPS/PPS must not reopen a config)", got)
	}
	if got := countRoleElems(elems, mediatree.RoleFrameVideo); got != 3 {
		t.Fatalf("frame(video) count = %d, want 3", got)
	}
}

// TestConfigTimestamp_IsFirstFrameTime_NotParamsTime is the zero-timestamp
// regression test: internal/ingest/rtsp.go never sets StreamParams.Time (it
// has no meaningful value to put there), yet the resulting config(video)
// node must carry the real first-frame time, not the zero value that ends
// up on the wire as Unix epoch.
func TestConfigTimestamp_IsFirstFrameTime_NotParamsTime(t *testing.T) {
	seg, _ := newTestSegment()
	p := NewCapturePolicy(1, seg, 1000, PolicyContinuous, PolicyParams{})
	p.SetStreamParams(0, fcontainer.KindVideo, videoParams(0)) // Time: 0, matches rtsp.go's real construction

	if err := p.StartRecording(100, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := p.HandleFrame(0, fcontainer.KindVideo, vframe(12345, mediatree.FrameKindI)); err != nil {
		t.Fatalf("HandleFrame: %v", err)
	}

	elems := seg.Elements()
	var got uint64
	found := false
	for _, e := range elems {
		if e.Role == mediatree.RoleConfigVideo && len(e.Value) == 8 {
			got = binary.LittleEndian.Uint64(e.Value)
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no config(video) node found")
	}
	if got != 12345 {
		t.Fatalf("config(video) timestamp = %d, want 12345 (the first frame's time), not 0", got)
	}
}
