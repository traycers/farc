package ingest

import (
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/headers"
	"github.com/pion/rtp"
	"testing"

	"traycers/farc/mediatree"
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
	rec := &fakeRecorder{}
	policy := NewCapturePolicy(1, newTestSegment(rec), uint64(1000), PolicyContinuous, PolicyParams{})
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

	err = policy.Close(2000)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(rec.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(rec.writes))
	}
	got := countRole(rec.writes[0].filler, mediatree.RoleFrameAudio)
	if got != 1 {
		t.Fatalf("frame(audio) count = %d, want 1 (the two frames sent while skipFrames()==true must not appear)", got)
	}
}
