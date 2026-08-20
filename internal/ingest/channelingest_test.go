package ingest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/headers"
	"github.com/pion/rtp"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

// onPacketOnlySource is a minimal rtspSource fake: setupG711 (like every
// setup* method) only ever calls OnPacketRTP on it, so nothing else needs a
// real implementation for this test.
type onPacketOnlySource struct {
	cb gortsplib.OnPacketRTPFunc
}

func (s *onPacketOnlySource) Start() error { return nil }
func (s *onPacketOnlySource) Close()       {}
func (s *onPacketOnlySource) Wait() error  { return nil }
func (s *onPacketOnlySource) Describe(*base.URL) (*description.Session, *base.Response, error) {
	return nil, nil, nil
}
func (s *onPacketOnlySource) Setup(*base.URL, *description.Media, int, int) (*base.Response, error) {
	return nil, nil
}
func (s *onPacketOnlySource) Play(*headers.Range) (*base.Response, error) { return nil, nil }
func (s *onPacketOnlySource) OnPacketRTP(medi *description.Media, forma format.Format, cb gortsplib.OnPacketRTPFunc) {
	s.cb = cb
}

func TestChannelIngest_SkipFramesWhileBackpressureSignalTrue(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	err := policy.StartRecording(0, nil)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)

	src := &onPacketOnlySource{}
	f := &format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1}
	ci.setupG711(src, &description.Media{}, f, 0)

	skip := true
	ci.SetBackpressureSignal(func() bool { return skip })

	src.cb(&rtp.Packet{Payload: []byte{1, 2, 3}})
	src.cb(&rtp.Packet{Payload: []byte{4, 5, 6}})

	skip = false
	src.cb(&rtp.Packet{Payload: []byte{7, 8, 9}})

	got := countRoleElems(seg.Elements(), mediatree.RoleFrameAudio)
	if got != 1 {
		t.Fatalf("frame(audio) count = %d, want 1 (the two frames sent while skipFrames()==true must not appear)", got)
	}

	err = policy.Close(2000)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestChannelIngest_RTSPBytesReceivedCountsRawPayloadBytes is
// .scratch/storage-fblocks-dashboard-v2/issues/02-rtsp-in-vs-storage-write-
// volume.md: the counter must reflect raw RTP payload bytes as they arrive,
// regardless of what the codec-specific decode step does with them.
func TestChannelIngest_RTSPBytesReceivedCountsRawPayloadBytes(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(1000), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)

	src := &onPacketOnlySource{}
	f := &format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1}
	ci.setupG711(src, &description.Media{}, f, 0)

	src.cb(&rtp.Packet{Payload: []byte{1, 2, 3}})
	src.cb(&rtp.Packet{Payload: []byte{4, 5, 6, 7, 8}})

	if got, want := ci.RTSPBytesReceived(), int64(8); got != want {
		t.Fatalf("RTSPBytesReceived() = %d, want %d", got, want)
	}

	if err := policy.Close(2000); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// dualMediaSource is an rtspSource fake that, unlike scriptedSource, records
// every OnPacketRTP callback it's given (in registration order) so a test
// can drive real per-media frames through ChannelIngest.run's actual
// setup loop, closing registered once every expected media has registered
// its callback.
type dualMediaSource struct {
	desc *description.Session
	want int

	closed    chan struct{}
	closeOnce sync.Once

	mu         sync.Mutex
	cbs        []gortsplib.OnPacketRTPFunc
	registered chan struct{}
}

func (s *dualMediaSource) Start() error { return nil }
func (s *dualMediaSource) Close()       { s.closeOnce.Do(func() { close(s.closed) }) }
func (s *dualMediaSource) Wait() error  { <-s.closed; return nil }
func (s *dualMediaSource) Describe(*base.URL) (*description.Session, *base.Response, error) {
	return s.desc, nil, nil
}
func (s *dualMediaSource) Setup(*base.URL, *description.Media, int, int) (*base.Response, error) {
	return nil, nil
}
func (s *dualMediaSource) Play(*headers.Range) (*base.Response, error) { return nil, nil }
func (s *dualMediaSource) OnPacketRTP(medi *description.Media, forma format.Format, cb gortsplib.OnPacketRTPFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cbs = append(s.cbs, cb)
	if len(s.cbs) == s.want {
		close(s.registered)
	}
}

// TestChannelIngest_RunPutsVideoAndAudioUnderTheSameStreamNode is
// .scratch/fblocks-ui/issues/07-one-stream-per-channel-video-and-audio.md:
// one RTSP link (one Describe/Setup/Play session) is one stream regardless
// of how many media kinds it carries -- video and audio must land under the
// same RoleStream node, not two separate ones numbered per track.
func TestChannelIngest_RunPutsVideoAndAudioUnderTheSameStreamNode(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(time.Second), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	ci.SetLogger(t.Logf)

	sps := []byte{0x67, 0x01, 0x02, 0x03} // type 7 (SPS)
	pps := []byte{0x68, 0x04}             // type 8 (PPS)
	idr := []byte{0x65, 0xaa, 0xaa}       // type 5 (IDR)
	h264 := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps, PPS: pps}
	desc := &description.Session{Medias: []*description.Media{
		{Type: description.MediaTypeVideo, Formats: []format.Format{h264}},
		{Type: description.MediaTypeAudio, Formats: []format.Format{&format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1}}},
	}}
	src := &dualMediaSource{desc: desc, want: 2, closed: make(chan struct{}), registered: make(chan struct{})}

	runErr := make(chan error, 1)
	go func() { runErr <- ci.run(context.Background(), src, &base.URL{}) }()

	select {
	case <-src.registered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for both media to register OnPacketRTP")
	}

	rtpEnc, err := h264.CreateEncoder()
	if err != nil {
		t.Fatalf("CreateEncoder: %v", err)
	}
	packets, err := rtpEnc.Encode([][]byte{sps, pps, idr})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, pkt := range packets {
		src.cbs[0](pkt) // video
	}
	src.cbs[1](&rtp.Packet{Payload: []byte{1, 2, 3}}) // audio

	src.Close()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after Close")
	}

	elems := seg.Elements()
	streamIDs := roleIDs(elems, mediatree.RoleStream)
	if len(streamIDs) != 1 {
		t.Fatalf("stream node count = %d, want 1 (one RTSP link must produce one stream, video and audio together)", len(streamIDs))
	}
	streamID := streamIDs[0]
	if _, ok := mediatree.FindChildByRole(elems, streamID, mediatree.RoleVideo); !ok {
		t.Fatal("stream node has no video child")
	}
	if _, ok := mediatree.FindChildByRole(elems, streamID, mediatree.RoleAudio); !ok {
		t.Fatal("stream node has no audio child")
	}
}

// TestChannelIngest_SetupMediaIgnoresSecondTrackOfSameKindOnOneLink covers
// the edge case from the same design decision: if one RTSP link's SDP
// somehow describes two tracks of the same media kind (not seen from real
// cameras, no explicit prohibition in gortsplib), the second is logged and
// ignored rather than silently overwriting the first's cached params.
func TestChannelIngest_SetupMediaIgnoresSecondTrackOfSameKindOnOneLink(t *testing.T) {
	seg, _ := newTestSegment()
	policy := NewCapturePolicy(1, seg, uint64(time.Second), PolicyContinuous, PolicyParams{})
	if err := policy.StartRecording(0, nil); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	ci := NewChannelIngest(1, policy)
	var logs []string
	ci.SetLogger(func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) })

	spsA, ppsA := []byte{0x67, 1, 2, 3}, []byte{0x68, 4}
	spsB, ppsB := []byte{0x67, 9, 9, 9}, []byte{0x68, 9}
	mediaA := &description.Media{Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: spsA, PPS: ppsA}}}
	mediaB := &description.Media{Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: spsB, PPS: ppsB}}}

	seen := make(map[fcontainer.StreamKind]bool)
	src := &onPacketOnlySource{}
	ci.setupMedia(src, mediaA, 0, seen)
	ci.setupMedia(src, mediaB, 0, seen)

	params, ok := policy.CachedParams(0, fcontainer.KindVideo)
	if !ok {
		t.Fatal("no cached video params after setupMedia")
	}
	if string(params.ParamSPS) != string(spsA) {
		t.Fatalf("cached video SPS = %x, want %x (the second track on the same link must be ignored, not overwrite the first)", params.ParamSPS, spsA)
	}
	if len(logs) == 0 {
		t.Fatal("expected a log line about the ignored second video track, got none")
	}
}

// TestChannelIngest_OnConnectionChange_CanSafelyCallBackIntoConnected guards
// the same class of self-deadlock fixed for CapturePolicy.onRecordingChange
// (see TestOnRecordingChange_CanSafelyCallBackIntoPolicy): the hook must be
// able to call any connMu-guarded method (Connected here) without
// deadlocking on the same, non-reentrant mutex.
func TestChannelIngest_OnConnectionChange_CanSafelyCallBackIntoConnected(t *testing.T) {
	ci := NewChannelIngest(1, nil)

	var got bool
	ci.SetOnConnectionChange(func(channel uint16, connected bool) {
		got = ci.Connected()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		ci.setConnected(true)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("setConnected did not return -- onConnectionChange likely still holds connMu while firing")
	}
	if !got {
		t.Fatal("onConnectionChange saw Connected() = false, want true")
	}
}
