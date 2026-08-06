package ingest

import (
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/headers"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"

	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
)

// rtspSource is the subset of *gortsplib.Client's surface ChannelIngest
// depends on (reference shape: temp/mediamtx-1.19.3/internal/staticsources/
// rtsp/source.go). *gortsplib.Client satisfies this directly; tests can
// inject a fake, or (for the one real end-to-end test) point a real Client
// at an in-process loopback gortsplib server.
type rtspSource interface {
	Start() error
	Close()
	Wait() error
	Describe(u *base.URL) (*description.Session, *base.Response, error)
	Setup(baseURL *base.URL, media *description.Media, rtpPort, rtcpPort int) (*base.Response, error)
	Play(ra *headers.Range) (*base.Response, error)
	OnPacketRTP(medi *description.Media, forma format.Format, cb gortsplib.OnPacketRTPFunc)
}

// NewClient builds a real gortsplib.Client plus its parsed base.URL for
// rtspURL, ready to pass to ChannelIngest.run (via Run).
func NewClient(rtspURL string, readTimeout, writeTimeout time.Duration) (*gortsplib.Client, *base.URL, error) {
	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return nil, nil, fmt.Errorf("ingest: parse RTSP URL: %w", err)
	}
	c := &gortsplib.Client{
		Scheme:       u.Scheme,
		Host:         u.Host,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	return c, u, nil
}

// setupMedia registers RTP->frame decoding for every format this package
// supports within medi, feeding decoded frames into ci.policy. Unsupported
// formats are skipped with a log line, not a hard error — one unsupported
// secondary format shouldn't take down an otherwise-usable channel.
func (ci *ChannelIngest) setupMedia(c rtspSource, medi *description.Media, streamNum uint32) {
	for _, forma := range medi.Formats {
		switch f := forma.(type) {
		case *format.H264:
			ci.setupH264(c, medi, f, streamNum)
		case *format.H265:
			ci.setupH265(c, medi, f, streamNum)
		case *format.G711:
			ci.setupG711(c, medi, f, streamNum)
		case *format.MPEG4Audio:
			ci.setupAAC(c, medi, f, streamNum)
		default:
			ci.logf("ingest: channel %d: unsupported RTSP format %T, skipping", ci.channel, forma)
		}
	}
}

// muxAnnexB is called once per decoded access unit -- the returned slice is
// retained by CapturePolicy's FrameQueue for the channel's whole retention
// window (and, while recording, by the open segment's Filler too), so it
// must be a single owned allocation, not a view into anything gortsplib
// reuses. Pre-sizing avoids the repeated grow-and-copy append(nil, ...)
// would otherwise do per NALU -- for a keyframe access unit (SPS+PPS+IDR)
// that's the difference between one allocation and several.
func muxAnnexB(nalus [][]byte) []byte {
	size := 0
	for _, n := range nalus {
		size += 4 + len(n)
	}
	out := make([]byte, 0, size)
	for _, n := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

func (ci *ChannelIngest) setupH264(c rtspSource, medi *description.Media, f *format.H264, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		ci.logf("ingest: channel %d: H264 decoder: %v", ci.channel, err)
		return
	}
	sps, pps := f.SPS, f.PPS
	ci.policy.SetStreamParams(streamNum, fcontainer.KindVideo, fcontainer.StreamParams{
		CodecVideo: mediatree.CodecH264, ParamSPS: sps, ParamPPS: pps,
	})

	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		au, err := dec.Decode(pkt)
		if err != nil {
			return // fragment/ordering errors are expected mid-stream
		}

		kind := uint8(mediatree.FrameKindP)
		changed := false
		for _, nalu := range au {
			if len(nalu) == 0 {
				continue
			}
			switch h264.NALUType(nalu[0] & 0x1f) {
			case h264.NALUTypeIDR:
				kind = mediatree.FrameKindI
			case h264.NALUTypeSPS:
				sps, changed = nalu, true
			case h264.NALUTypePPS:
				pps, changed = nalu, true
			}
		}
		if changed {
			ci.policy.SetStreamParams(streamNum, fcontainer.KindVideo, fcontainer.StreamParams{
				CodecVideo: mediatree.CodecH264, ParamSPS: sps, ParamPPS: pps,
			})
		}

		if ci.skipFrames() {
			return
		}
		frame := fcontainer.Frame{Data: muxAnnexB(au), Time: ci.nowNS(), Kind: kind}
		if err := ci.policy.HandleFrame(streamNum, fcontainer.KindVideo, frame); err != nil {
			ci.logf("ingest: channel %d: %v", ci.channel, err)
		}
	})
}

func (ci *ChannelIngest) setupH265(c rtspSource, medi *description.Media, f *format.H265, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		ci.logf("ingest: channel %d: H265 decoder: %v", ci.channel, err)
		return
	}
	vps, sps, pps := f.VPS, f.SPS, f.PPS
	ci.policy.SetStreamParams(streamNum, fcontainer.KindVideo, fcontainer.StreamParams{
		CodecVideo: mediatree.CodecH265, ParamVPS: vps, ParamSPS: sps, ParamPPS: pps,
	})

	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		au, err := dec.Decode(pkt)
		if err != nil {
			return
		}

		kind := uint8(mediatree.FrameKindP)
		if h265.IsRandomAccess(au) {
			kind = mediatree.FrameKindI
		}
		changed := false
		for _, nalu := range au {
			if len(nalu) == 0 {
				continue
			}
			switch h265.NALUType((nalu[0] >> 1) & 0b111111) {
			case h265.NALUType_VPS_NUT:
				vps, changed = nalu, true
			case h265.NALUType_SPS_NUT:
				sps, changed = nalu, true
			case h265.NALUType_PPS_NUT:
				pps, changed = nalu, true
			}
		}
		if changed {
			ci.policy.SetStreamParams(streamNum, fcontainer.KindVideo, fcontainer.StreamParams{
				CodecVideo: mediatree.CodecH265, ParamVPS: vps, ParamSPS: sps, ParamPPS: pps,
			})
		}

		if ci.skipFrames() {
			return
		}
		frame := fcontainer.Frame{Data: muxAnnexB(au), Time: ci.nowNS(), Kind: kind}
		if err := ci.policy.HandleFrame(streamNum, fcontainer.KindVideo, frame); err != nil {
			ci.logf("ingest: channel %d: %v", ci.channel, err)
		}
	})
}

func (ci *ChannelIngest) setupG711(c rtspSource, medi *description.Media, f *format.G711, streamNum uint32) {
	codec := mediatree.CodecG711U
	if !f.MULaw {
		codec = mediatree.CodecG711A
	}
	ci.policy.SetStreamParams(streamNum, fcontainer.KindAudio, fcontainer.StreamParams{
		CodecAudio: codec, SampleRate: uint32(f.SampleRate), ChannelCount: uint8(f.ChannelCount),
	})

	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		if ci.skipFrames() {
			return
		}
		frame := fcontainer.Frame{Data: append([]byte(nil), pkt.Payload...), Time: ci.nowNS()}
		if err := ci.policy.HandleFrame(streamNum, fcontainer.KindAudio, frame); err != nil {
			ci.logf("ingest: channel %d: %v", ci.channel, err)
		}
	})
}

func (ci *ChannelIngest) setupAAC(c rtspSource, medi *description.Media, f *format.MPEG4Audio, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		ci.logf("ingest: channel %d: AAC decoder: %v", ci.channel, err)
		return
	}
	asc, err := f.Config.Marshal()
	if err != nil {
		ci.logf("ingest: channel %d: AAC config: %v", ci.channel, err)
		return
	}
	ci.policy.SetStreamParams(streamNum, fcontainer.KindAudio, fcontainer.StreamParams{
		CodecAudio: mediatree.CodecAAC, SampleRate: uint32(f.Config.SampleRate),
		ChannelCount: uint8(f.Config.ChannelCount), ParamAudioConfig: asc,
	})

	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		aus, err := dec.Decode(pkt)
		if err != nil {
			return
		}
		if ci.skipFrames() {
			return
		}
		for _, au := range aus {
			frame := fcontainer.Frame{Data: append([]byte(nil), au...), Time: ci.nowNS()}
			if err := ci.policy.HandleFrame(streamNum, fcontainer.KindAudio, frame); err != nil {
				ci.logf("ingest: channel %d: %v", ci.channel, err)
			}
		}
	})
}
