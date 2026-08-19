package fcontainer

import "github.com/traycers/farc/mediatree"

// StreamKind selects which branch (video/audio) AddStreamParams/AddFrames
// operate on (docs/docs/archive/07-media-tree.md §2).
type StreamKind uint8

const (
	KindVideo StreamKind = iota
	KindAudio
)

func (k StreamKind) String() string {
	if k == KindVideo {
		return "video"
	}
	return "audio"
}

// StreamParams carries one moment's codec parameters for AddStreamParams.
// Which fields apply depends on Kind (docs/docs/archive/07-media-tree.md
// §3.2 video / §3.3 audio); the other kind's fields are ignored.
type StreamParams struct {
	// Time is the moment these parameters take effect (the new config
	// node's value, a timestamp — not a version number).
	Time uint64

	// Video (Kind == KindVideo).
	CodecVideo   uint8   // mediatree.CodecH264 or CodecH265, required
	ParamVPS     []byte  // optional, H.265 only
	ParamSPS     []byte  // required
	ParamPPS     []byte  // required
	Framerate    float64 // optional; 0 means absent
	HasFramerate bool
	// Width/Height are the stream's resolution, parsed from ParamSPS at
	// ingest time -- optional (0 means absent/unparseable; unlike Framerate,
	// 0 is never a valid resolution so no separate Has* flag is needed).
	Width  uint32
	Height uint32

	// Audio (Kind == KindAudio).
	CodecAudio       uint8  // mediatree.CodecPCM/AAC/G711A/G711U, required
	SampleRate       uint32 // required, Hz
	ChannelCount     uint8  // required
	ParamAudioConfig []byte // optional, AAC only

	// Shared (both kinds, optional).
	CodecProfile []byte
}

// Validate checks the required-field rules from
// docs/docs/archive/07-media-tree.md §3.2/§3.3 for the given kind.
func (p StreamParams) Validate(kind StreamKind) error {
	switch kind {
	case KindVideo:
		if p.CodecVideo != mediatree.CodecH264 && p.CodecVideo != mediatree.CodecH265 {
			return errInvalidVideoCodec
		}
		if len(p.ParamSPS) == 0 {
			return errMissingSPS
		}
		if len(p.ParamPPS) == 0 {
			return errMissingPPS
		}
		if p.CodecVideo == mediatree.CodecH264 && len(p.ParamVPS) != 0 {
			return errVPSOnH264
		}
	case KindAudio:
		switch p.CodecAudio {
		case mediatree.CodecPCM, mediatree.CodecAAC, mediatree.CodecG711A, mediatree.CodecG711U:
		default:
			return errInvalidAudioCodec
		}
		if p.SampleRate == 0 {
			return errMissingSampleRate
		}
		if p.ChannelCount == 0 {
			return errMissingChannelCount
		}
		if p.CodecAudio != mediatree.CodecAAC && len(p.ParamAudioConfig) != 0 {
			return errAudioConfigNotAAC
		}
	default:
		return errUnknownKind
	}
	return nil
}

// Frame is one encoded access unit passed to AddFrames.
type Frame struct {
	Data []byte
	Time uint64 // Unix ns, absolute
	Kind uint8  // mediatree.FrameKindI/FrameKindP; ignored for audio
}
