package fcontainer

import "errors"

var (
	errInvalidVideoCodec   = errors.New("fcontainer: video codec must be CodecH264 or CodecH265")
	errMissingSPS          = errors.New("fcontainer: video params missing required param_sps")
	errMissingPPS          = errors.New("fcontainer: video params missing required param_pps")
	errVPSOnH264           = errors.New("fcontainer: param_vps is H.265-only, must be empty for H.264")
	errInvalidAudioCodec   = errors.New("fcontainer: audio codec must be one of CodecPCM/CodecAAC/CodecG711A/CodecG711U")
	errMissingSampleRate   = errors.New("fcontainer: audio params missing required sample_rate")
	errMissingChannelCount = errors.New("fcontainer: audio params missing required channel_count")
	errAudioConfigNotAAC   = errors.New("fcontainer: param_audio_config is AAC-only")
	errUnknownKind         = errors.New("fcontainer: unknown StreamKind")
	errStaleConfigID       = errors.New("fcontainer: configID does not refer to a config node created by this Filler")
)
