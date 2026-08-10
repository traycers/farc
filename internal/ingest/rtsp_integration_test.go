package ingest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"

	"traycers/farc/mediatree"
)

// testServerHandler is the minimal gortsplib.ServerHandler needed to serve
// one pre-built ServerStream to any connecting client (reference shape:
// gortsplib's own examples/server-play-format-h264-from-disk).
type testServerHandler struct {
	stream *gortsplib.ServerStream
}

func (h *testServerHandler) OnDescribe(*gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *testServerHandler) OnSetup(*gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *testServerHandler) OnPlay(*gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// TestChannelIngest_RealRTSPServerH264EndToEnd is PLAN.md's Phase 9
// "slower test": a real in-process gortsplib RTSP server over loopback
// serves synthetic H.264 (SPS/PPS carried in-band on the keyframe, as real
// cameras commonly do — the SDP-advertised format below carries neither),
// and a real *gortsplib.Client (via ChannelIngest.Run, not a fake) decodes
// it end to end into a Filler's Content tree, asserting the expected
// SPS/PPS/frame/GOP shape.
func TestChannelIngest_RealRTSPServerH264EndToEnd(t *testing.T) {
	handler := &testServerHandler{}
	server := &gortsplib.Server{
		Handler:     handler,
		RTSPAddress: "127.0.0.1:0",
	}
	err := server.Start()
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	defer server.Close()

	desc := &description.Session{
		Medias: []*description.Media{{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1}},
		}},
	}
	stream := &gortsplib.ServerStream{Server: server, Desc: desc}
	err = stream.Initialize()
	if err != nil {
		t.Fatalf("stream.Initialize: %v", err)
	}
	defer stream.Close()
	handler.stream = stream

	rtpEnc, err := desc.Medias[0].Formats[0].(*format.H264).CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}

	rec := &fakeRecorder{}
	policy := NewCapturePolicy(1, rec, uint64(10*time.Second), PolicyContinuous, PolicyParams{})
	err = policy.StartRecording(0, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	addr := server.NetListener().Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- ci.Run(ctx, fmt.Sprintf("rtsp://%s/test", addr), 2*time.Second, 2*time.Second)
	}()

	// Give the client time to DESCRIBE/SETUP/PLAY before the server writes
	// any RTP -- ServerStream is a live broadcast, not a DVR: packets
	// written before a session is playing are simply never delivered.
	time.Sleep(300 * time.Millisecond)

	// Synthetic H.264 NALUs -- content is meaningless, only the NALU type
	// nibble (low 5 bits of the first byte) matters for this test.
	sps := []byte{0x67, 0x01, 0x02, 0x03} // type 7 (SPS)
	pps := []byte{0x68, 0x04}             // type 8 (PPS)
	idr := []byte{0x65, 0xaa, 0xaa}       // type 5 (IDR)
	p1 := []byte{0x41, 0xbb}              // type 1 (non-IDR)
	p2 := []byte{0x41, 0xcc}              // type 1 (non-IDR)
	aus := [][][]byte{{sps, pps, idr}, {p1}, {p2}}

	var ts uint32
	for _, au := range aus {
		packets, err := rtpEnc.Encode(au)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for _, pkt := range packets {
			pkt.Timestamp = ts
			err := stream.WritePacketRTP(desc.Medias[0], pkt)
			if err != nil {
				t.Fatalf("WritePacketRTP: %v", err)
			}
		}
		ts += 3000 // 90kHz clock, ~33ms/frame
	}

	// Give the loopback round-trip time to deliver and decode all three
	// access units, then stop the channel — Run's ctx.Done() path closes
	// the segment, handing the finished Filler to rec.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("ChannelIngest.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ChannelIngest.Run did not return after cancel")
	}

	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	filler := rec.writes[0].filler
	elems := filler.Elements()

	configID := findConfigVideo(t, elems)
	dataID, ok := mediatree.FindChildByRole(elems, configID, mediatree.RoleDataVideo)
	if !ok {
		t.Fatal("no data(video) node found")
	}
	spsID, ok := mediatree.FindChildByRole(elems, dataID, mediatree.RoleParamSPS)
	if !ok {
		t.Fatal("no param_sps node found")
	}
	if string(elems[spsID].Value) != string(sps) {
		t.Fatalf("SPS = %x, want %x", elems[spsID].Value, sps)
	}

	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 3 {
		t.Fatalf("frame(video) count = %d, want 3", len(frameIDs))
	}
	kindOf := func(frameID uint32) uint8 {
		kindID, ok := mediatree.FindChildByRole(elems, frameID, mediatree.RoleFrameKind)
		if !ok || len(elems[kindID].Value) != 1 {
			t.Fatalf("frame %d: no frame_kind child", frameID)
		}
		return elems[kindID].Value[0]
	}
	if kindOf(frameIDs[0]) != mediatree.FrameKindI {
		t.Fatalf("first frame kind = %v, want I (GOP start)", kindOf(frameIDs[0]))
	}
	for _, id := range frameIDs[1:] {
		if kindOf(id) != mediatree.FrameKindP {
			t.Fatalf("frame %d kind = %v, want P", id, kindOf(id))
		}
	}
}

// TestChannelIngest_RepeatedSPSPPSDoesNotDuplicateConfig reproduces the real-
// world pattern that used to duplicate config(video) nodes: a camera that
// re-announces byte-identical SPS/PPS before every IDR frame (two GOPs here,
// each led by the exact same sps/pps bytes) must still produce exactly one
// config(video) version, not one per GOP.
func TestChannelIngest_RepeatedSPSPPSDoesNotDuplicateConfig(t *testing.T) {
	handler := &testServerHandler{}
	server := &gortsplib.Server{
		Handler:     handler,
		RTSPAddress: "127.0.0.1:0",
	}
	err := server.Start()
	if err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	defer server.Close()

	desc := &description.Session{
		Medias: []*description.Media{{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1}},
		}},
	}
	stream := &gortsplib.ServerStream{Server: server, Desc: desc}
	err = stream.Initialize()
	if err != nil {
		t.Fatalf("stream.Initialize: %v", err)
	}
	defer stream.Close()
	handler.stream = stream

	rtpEnc, err := desc.Medias[0].Formats[0].(*format.H264).CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}

	rec := &fakeRecorder{}
	policy := NewCapturePolicy(1, rec, uint64(10*time.Second), PolicyContinuous, PolicyParams{})
	err = policy.StartRecording(0, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	addr := server.NetListener().Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runErr := make(chan error, 1)
	go func() {
		runErr <- ci.Run(ctx, fmt.Sprintf("rtsp://%s/test", addr), 2*time.Second, 2*time.Second)
	}()

	time.Sleep(300 * time.Millisecond)

	// Same sps/pps bytes re-sent before the second GOP's IDR -- exactly what
	// many real cameras do, and what previously fooled ensureConfigLocked's
	// pointer-identity check into opening a second config(video).
	sps := []byte{0x67, 0x01, 0x02, 0x03}
	pps := []byte{0x68, 0x04}
	idr1 := []byte{0x65, 0xaa, 0xaa}
	p1 := []byte{0x41, 0xbb}
	idr2 := []byte{0x65, 0xdd, 0xdd}
	p2 := []byte{0x41, 0xcc}
	aus := [][][]byte{{sps, pps, idr1}, {p1}, {sps, pps, idr2}, {p2}}

	var ts uint32
	for _, au := range aus {
		packets, err := rtpEnc.Encode(au)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for _, pkt := range packets {
			pkt.Timestamp = ts
			err := stream.WritePacketRTP(desc.Medias[0], pkt)
			if err != nil {
				t.Fatalf("WritePacketRTP: %v", err)
			}
		}
		ts += 3000
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("ChannelIngest.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ChannelIngest.Run did not return after cancel")
	}

	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	elems := rec.writes[0].filler.Elements()

	configIDs := roleIDs(elems, mediatree.RoleConfigVideo)
	if len(configIDs) != 1 {
		t.Fatalf("config(video) count = %d, want 1 (repeated byte-identical SPS/PPS must not open a new version)", len(configIDs))
	}

	frameIDs := roleIDs(elems, mediatree.RoleFrameVideo)
	if len(frameIDs) != 4 {
		t.Fatalf("frame(video) count = %d, want 4", len(frameIDs))
	}
}

func roleIDs(elems []mediatree.Element, role mediatree.Role) []uint32 {
	var out []uint32
	for i, e := range elems {
		if e.Role == role {
			out = append(out, uint32(i))
		}
	}
	return out
}

func findConfigVideo(t *testing.T, elems []mediatree.Element) uint32 {
	t.Helper()
	ids := roleIDs(elems, mediatree.RoleConfigVideo)
	if len(ids) == 0 {
		t.Fatal("no config(video) node found")
	}
	return ids[0]
}
