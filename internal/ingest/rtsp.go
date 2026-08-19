package ingest

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtph265"
	"github.com/bluenviron/gortsplib/v4/pkg/headers"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/mediatree"
)

// rtspSource is the subset of *gortsplib.Client's surface ChannelIngest
// depends on (reference shape: temp/mediamtx-1.19.3/internal/staticsources/
// rtsp/source.go). *gortsplib.Client satisfies this directly; tests can
// inject a fake, or (for the one real end-to-end test) point a real Client
// at an in-process loopback gortsplib server.
type rtspSource interface {
	Start(scheme, host string) error
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
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	return c, u, nil
}

// setupMedia registers RTP->frame decoding for every format this package
// supports within medi, feeding decoded frames into ci.policy. Unsupported
// formats are skipped with a log line, not a hard error — one unsupported
// secondary format shouldn't take down an otherwise-usable channel.
//
// seenKind tracks, across every medi in the current RTSP link's SDP, which
// fcontainer.StreamKind has already claimed streamNum (.scratch/fblocks-ui/
// issues/07-one-stream-per-channel-video-and-audio.md: one link is one
// stream, so at most one video and one audio track can live under it, per
// docs/docs/archive/07-media-tree.md's "не более одного узла каждого
// вида"). A second track of an already-claimed kind is logged and ignored
// -- not expected from real cameras, but nothing in gortsplib forbids it,
// and silently overwriting the first track's cached params would be worse
// than dropping the second.
func (ci *ChannelIngest) setupMedia(c rtspSource, medi *description.Media, streamNum uint32, seenKind map[fcontainer.StreamKind]bool) {
	claim := func(kind fcontainer.StreamKind, formatName string) bool {
		if seenKind[kind] {
			levellog.New(ci.logf).Warn("ingest: channel %d: stream %d already has a %v track, ignoring extra %s track", ci.channel, streamNum, kind, formatName)
			return false
		}
		seenKind[kind] = true
		return true
	}
	for _, forma := range medi.Formats {
		switch f := forma.(type) {
		case *format.H264:
			if claim(fcontainer.KindVideo, "H264") {
				ci.setupH264(c, medi, f, streamNum)
			}
		case *format.H265:
			if claim(fcontainer.KindVideo, "H265") {
				ci.setupH265(c, medi, f, streamNum)
			}
		case *format.G711:
			if claim(fcontainer.KindAudio, "G711") {
				ci.setupG711(c, medi, f, streamNum)
			}
		case *format.MPEG4Audio:
			if claim(fcontainer.KindAudio, "AAC") {
				ci.setupAAC(c, medi, f, streamNum)
			}
		default:
			levellog.New(ci.logf).Warn("ingest: channel %d: unsupported RTSP format %T, skipping", ci.channel, forma)
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

// keyframeWaitTimeout bounds how long keyframeGate waits before warning
// that no keyframe has arrived at all -- the pathological case (broken
// camera GOP config, no IDR ever sent). A var, not a const, so tests can
// shrink it instead of waiting out a real 30s.
var keyframeWaitTimeout = 30 * time.Second

// keyframeGate drops a video stream's leading frames until the first
// keyframe arrives (.scratch/capture-keyframe-start/issues/
// 01-first-frame-not-guaranteed-keyframe.md) -- a P-frame with no
// preceding I-frame can't be decoded, so recording it would just corrupt
// the fcontainer's very first frame. Shared by setupH264/setupH265, which
// each construct their own instance (via newKeyframeGate) per RTSP
// session, so the gate re-arms on every reconnect, not just the first
// connect.
//
// seen is an atomic.Bool, not a plain bool, because keyframeWaitTimeout's
// warning fires from time.AfterFunc's own goroutine, concurrently with
// allow (called from the packet-decode callback's goroutine) -- dropped
// is fine as a plain int since only allow ever touches it.
type keyframeGate struct {
	seen    atomic.Bool
	dropped int
}

// newKeyframeGate starts a gate and arms its pathological-case timeout: if
// no keyframe has arrived by the time it fires, log a warning so "connected
// but recording nothing" doesn't stay silently invisible.
func newKeyframeGate(channel uint16, logf func(format string, args ...any)) *keyframeGate {
	g := &keyframeGate{}
	time.AfterFunc(keyframeWaitTimeout, func() {
		if !g.seen.Load() {
			levellog.New(logf).Warn("ingest: channel %d: no keyframe received within %s of connecting -- check the camera's GOP configuration", channel, keyframeWaitTimeout)
		}
	})
	return g
}

// allow reports whether kind may proceed to HandleFrame. Once the first
// keyframe arrives, it logs how many leading frames were dropped waiting
// for it (routine case -- normally a handful at most).
func (g *keyframeGate) allow(kind uint8, channel uint16, logf func(format string, args ...any)) bool {
	if g.seen.Load() {
		return true
	}
	if kind != mediatree.FrameKindI {
		g.dropped++
		return false
	}
	if g.dropped > 0 {
		levellog.New(logf).Warn("ingest: channel %d: dropped %d leading non-keyframe frame(s) waiting for first keyframe", channel, g.dropped)
	}
	g.seen.Store(true)
	return true
}

// gopShedGate latches a backpressure-shedding decision for a whole GOP,
// re-evaluating skipFrames() only when a keyframe arrives
// (.scratch/capture-keyframe-start/issues/02-backpressure-gop-aware-
// shedding.md) -- checking it per-frame (as this package does for audio,
// which has no GOP concept) could otherwise split a GOP between recorded
// and dropped halves, stranding P-frames without their reference I-frame.
// Video only; constructed fresh per RTSP session by setupH264/setupH265,
// same as keyframeGate, though its state doesn't actually need to survive
// a reconnect (it holds no more than "is the current GOP being shed").
type gopShedGate struct {
	shedding bool
}

// allow reports whether kind may proceed. skipFrames is only called when
// kind is a keyframe; every other frame of that GOP reuses the same
// answer, even if skipFrames would return something different by then.
func (g *gopShedGate) allow(kind uint8, skipFrames func() bool) bool {
	if kind == mediatree.FrameKindI {
		g.shedding = skipFrames()
	}
	return !g.shedding
}

// videoCodecStrategy captures the only things that actually differ between
// H264 and H265 setup: how to decode a packet into NALUs, the params to
// report before any packet has arrived, and how to classify a decoded AU
// (frame kind + any in-band parameter-set change). Everything else --
// keyframe gating, GOP-aware backpressure shedding, dispatch into
// CapturePolicy -- is identical and lives once, in setupVideo.
type videoCodecStrategy interface {
	decode(pkt *rtp.Packet) ([][]byte, error)
	initialParams() fcontainer.StreamParams
	// classify scans au's NALUs for this codec's frame-kind signal and any
	// changed parameter set. changed is false if nothing changed, in which
	// case changedParams is the zero value and must be ignored.
	classify(au [][]byte) (kind uint8, changedParams fcontainer.StreamParams, changed bool)
}

// setupVideo wires c's RTP packets for streamNum through strategy's
// codec-specific decode/classify, then the shared keyframe gate,
// backpressure shed, and CapturePolicy dispatch -- setupH264/setupH265 are
// thin constructors around this.
func (ci *ChannelIngest) setupVideo(c rtspSource, medi *description.Media, f format.Format, strategy videoCodecStrategy, streamNum uint32) {
	ci.reportVideoParams(streamNum, strategy.initialParams())

	gate := newKeyframeGate(ci.channel, ci.logf)
	shed := &gopShedGate{}
	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		au, err := strategy.decode(pkt)
		if err != nil {
			return // fragment/ordering errors are expected mid-stream
		}

		// Some cameras never advertise sprop-parameter-sets in SDP at all,
		// only sending them in-band before every IDR -- classify must keep
		// watching for that (reportVideoParams's own semantic comparison,
		// not this check, is what stops repeated identical resends from
		// reopening a config node; see reportVideoParams's doc comment).
		kind, changedParams, changed := strategy.classify(au)
		if changed {
			ci.reportVideoParams(streamNum, changedParams)
		}

		if !gate.allow(kind, ci.channel, ci.logf) {
			return
		}

		if !shed.allow(kind, ci.skipFrames) {
			return
		}
		frame := fcontainer.Frame{Data: muxAnnexB(au), Time: ci.nowNS(), Kind: kind}
		err = ci.policy.HandleFrame(streamNum, fcontainer.KindVideo, frame)
		if err != nil {
			levellog.New(ci.logf).Warn("ingest: channel %d: %v", ci.channel, err)
		}
	})
}

// h264Strategy is videoCodecStrategy for H264: kind comes from a per-NALU
// switch (an IDR NALU makes the whole AU a keyframe).
type h264Strategy struct {
	dec      *rtph264.Decoder
	sps, pps []byte
}

func (s *h264Strategy) decode(pkt *rtp.Packet) ([][]byte, error) { return s.dec.Decode(pkt) }

func (s *h264Strategy) initialParams() fcontainer.StreamParams {
	return buildVideoParams(mediatree.CodecH264, nil, s.sps, s.pps)
}

func (s *h264Strategy) classify(au [][]byte) (kind uint8, changedParams fcontainer.StreamParams, changed bool) {
	kind = mediatree.FrameKindP
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1f) { //nolint:exhaustive // only IDR/SPS/PPS carry information this loop needs; every other NALU type is a deliberate no-op
		case h264.NALUTypeIDR:
			kind = mediatree.FrameKindI
		case h264.NALUTypeSPS:
			s.sps, changed = nalu, true
		case h264.NALUTypePPS:
			s.pps, changed = nalu, true
		}
	}
	if changed {
		changedParams = buildVideoParams(mediatree.CodecH264, nil, s.sps, s.pps)
	}
	return kind, changedParams, changed
}

func (ci *ChannelIngest) setupH264(c rtspSource, medi *description.Media, f *format.H264, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		levellog.New(ci.logf).Error("ingest: channel %d: H264 decoder: %v", ci.channel, err)
		return
	}
	ci.setupVideo(c, medi, f, &h264Strategy{dec: dec, sps: f.SPS, pps: f.PPS}, streamNum)
}

// h265Strategy is videoCodecStrategy for H265: unlike H264, kind comes from
// h265.IsRandomAccess over the whole AU, not a single NALU type -- this
// difference is deliberate, not unified away.
type h265Strategy struct {
	dec           *rtph265.Decoder
	vps, sps, pps []byte
}

func (s *h265Strategy) decode(pkt *rtp.Packet) ([][]byte, error) { return s.dec.Decode(pkt) }

func (s *h265Strategy) initialParams() fcontainer.StreamParams {
	return buildVideoParams(mediatree.CodecH265, s.vps, s.sps, s.pps)
}

func (s *h265Strategy) classify(au [][]byte) (kind uint8, changedParams fcontainer.StreamParams, changed bool) {
	kind = mediatree.FrameKindP
	if h265.IsRandomAccess(au) {
		kind = mediatree.FrameKindI
	}
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h265.NALUType((nalu[0] >> 1) & 0b111111) { //nolint:exhaustive // only VPS/SPS/PPS carry information this loop needs; every other NALU type is a deliberate no-op
		case h265.NALUType_VPS_NUT:
			s.vps, changed = nalu, true
		case h265.NALUType_SPS_NUT:
			s.sps, changed = nalu, true
		case h265.NALUType_PPS_NUT:
			s.pps, changed = nalu, true
		}
	}
	if changed {
		changedParams = buildVideoParams(mediatree.CodecH265, s.vps, s.sps, s.pps)
	}
	return kind, changedParams, changed
}

func (ci *ChannelIngest) setupH265(c rtspSource, medi *description.Media, f *format.H265, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		levellog.New(ci.logf).Error("ingest: channel %d: H265 decoder: %v", ci.channel, err)
		return
	}
	ci.setupVideo(c, medi, f, &h265Strategy{dec: dec, vps: f.VPS, sps: f.SPS, pps: f.PPS}, streamNum)
}

// buildVideoParams builds a fcontainer.StreamParams for one negotiated
// H264/H265 video config, computing Width/Height from sps via mediacommon
// on a best-effort basis -- an unparseable SPS just leaves them 0, matching
// StreamParams' own "0 means absent" convention for these optional fields.
func buildVideoParams(codec uint8, vps, sps, pps []byte) fcontainer.StreamParams {
	p := fcontainer.StreamParams{CodecVideo: codec, ParamVPS: vps, ParamSPS: sps, ParamPPS: pps}
	switch codec {
	case mediatree.CodecH264:
		var s h264.SPS
		err := s.Unmarshal(sps)
		if err == nil {
			p.Width, p.Height = uint32(s.Width()), uint32(s.Height())
		}
	case mediatree.CodecH265:
		var s h265.SPS
		err := s.Unmarshal(sps)
		if err == nil {
			p.Width, p.Height = uint32(s.Width()), uint32(s.Height())
		}
	}
	return p
}

// videoParamsChanged reports whether next represents a genuine codec-
// parameter change from old, per this package's comparison rule: codec +
// resolution when both sides parsed a resolution, falling back to a raw
// SPS/PPS byte comparison when either side's resolution is unavailable
// (an unparseable SPS) -- resolution alone can't distinguish "unchanged"
// from "unknown" in that case, so the bytes are the only remaining signal
// that something really did change.
func videoParamsChanged(old, next fcontainer.StreamParams) bool {
	if old.CodecVideo != next.CodecVideo {
		return true
	}
	if old.Width != 0 && old.Height != 0 && next.Width != 0 && next.Height != 0 {
		return old.Width != next.Width || old.Height != next.Height
	}
	return !bytes.Equal(old.ParamSPS, next.ParamSPS) || !bytes.Equal(old.ParamPPS, next.ParamPPS)
}

// audioParamsChanged reports a genuine audio config change: codec, sample
// rate, and channel count -- this package's agreed comparison rule for
// audio (deliberately not ParamAudioConfig/CodecProfile bytes).
func audioParamsChanged(old, next fcontainer.StreamParams) bool {
	return old.CodecAudio != next.CodecAudio || old.SampleRate != next.SampleRate || old.ChannelCount != next.ChannelCount
}

// reportVideoParams/reportAudioParams call SetStreamParams only when params
// represents a genuine change from (streamNum, kind)'s currently cached
// params, or when nothing is cached yet for it -- this is the *only* place
// SetStreamParams is called for either kind, whether the caller is initial
// RTSP setup, a reconnect (channelingest.go's retry loop re-runs setupMedia
// from scratch), or (video only) an in-band SPS/PPS/VPS NALU seen mid-
// session. That last case matters because some cameras never advertise
// sprop-parameter-sets in SDP at all, relying entirely on in-band NALUs
// (see setupH264/setupH265) -- but many *other* cameras resend identical
// SPS/PPS in-band before every IDR frame regardless of whether SDP already
// had them, which used to spuriously reopen a new config node on nearly
// every keyframe. The semantic comparison here, not the caller, is what
// tells those two cases apart: a genuinely new stream's first real params
// differ from the empty/previous cached entry and get reported; a
// byte-identical resend does not.
func (ci *ChannelIngest) reportVideoParams(streamNum uint32, params fcontainer.StreamParams) {
	if cached, ok := ci.policy.CachedParams(streamNum, fcontainer.KindVideo); ok && !videoParamsChanged(cached, params) {
		return
	}
	ci.policy.SetStreamParams(streamNum, fcontainer.KindVideo, params)
}

func (ci *ChannelIngest) reportAudioParams(streamNum uint32, params fcontainer.StreamParams) {
	if cached, ok := ci.policy.CachedParams(streamNum, fcontainer.KindAudio); ok && !audioParamsChanged(cached, params) {
		return
	}
	ci.policy.SetStreamParams(streamNum, fcontainer.KindAudio, params)
}

func (ci *ChannelIngest) setupG711(c rtspSource, medi *description.Media, f *format.G711, streamNum uint32) {
	codec := mediatree.CodecG711U
	if !f.MULaw {
		codec = mediatree.CodecG711A
	}
	ci.reportAudioParams(streamNum, fcontainer.StreamParams{
		CodecAudio: codec, SampleRate: uint32(f.SampleRate), ChannelCount: uint8(f.ChannelCount),
	})

	c.OnPacketRTP(medi, f, func(pkt *rtp.Packet) {
		if ci.skipFrames() {
			return
		}
		frame := fcontainer.Frame{Data: append([]byte(nil), pkt.Payload...), Time: ci.nowNS()}
		err := ci.policy.HandleFrame(streamNum, fcontainer.KindAudio, frame)
		if err != nil {
			levellog.New(ci.logf).Warn("ingest: channel %d: %v", ci.channel, err)
		}
	})
}

func (ci *ChannelIngest) setupAAC(c rtspSource, medi *description.Media, f *format.MPEG4Audio, streamNum uint32) {
	dec, err := f.CreateDecoder()
	if err != nil {
		levellog.New(ci.logf).Error("ingest: channel %d: AAC decoder: %v", ci.channel, err)
		return
	}
	asc, err := f.Config.Marshal()
	if err != nil {
		levellog.New(ci.logf).Error("ingest: channel %d: AAC config: %v", ci.channel, err)
		return
	}
	ci.reportAudioParams(streamNum, fcontainer.StreamParams{
		CodecAudio: mediatree.CodecAAC, SampleRate: uint32(f.Config.SampleRate),
		ChannelCount: uint8(f.Config.ChannelCount), ParamAudioConfig: asc, //nolint:staticcheck // f.Config.ChannelConfig is a raw MPEG-4 config code (7 means 8 channels, not 7) -- ChannelCount is the actual count StreamParams needs, deprecated field or not
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
			err := ci.policy.HandleFrame(streamNum, fcontainer.KindAudio, frame)
			if err != nil {
				levellog.New(ci.logf).Warn("ingest: channel %d: %v", ci.channel, err)
			}
		}
	})
}
