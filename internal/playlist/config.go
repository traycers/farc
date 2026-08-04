package playlist

import (
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// videoSig and audioSig are comparable snapshots of a channel's active
// codec config. playlist has no access to farcd's Content bytes (it depends
// only on tocindex/toc/mediatree, per PLAN.md's package layout) so an exact
// SPS/PPS/ASC byte comparison isn't possible here; comparing codec plus each
// variable-width param's Size (available directly from the TOC's Size
// column, no Content read needed) is the size-based heuristic this package
// uses instead — a difference in encoded size means the bytes necessarily
// differ, which covers the common real case (resolution/profile change).
type videoSig struct {
	Codec   uint8
	SPSSize uint64
	PPSSize uint64
}

type audioSig struct {
	Codec   uint8
	ASCSize uint64
}

// configSigFor picks the first (first=true) or last (first=false)
// occurrence of role within [start,stop) and, for a variable-width sibling
// role paramRole, its Size. Shared by videoSigFor/audioSigFor.
func pickIndex(n int, first bool) int {
	if first {
		return 0
	}
	return n - 1
}

func videoSigFor(c *toc.Columns, start, stop uint32, first bool) (videoSig, bool) {
	codecIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleCodecVideo), start, stop)
	if len(codecIDs) == 0 {
		return videoSig{}, false
	}
	var sig videoSig
	if v, ok := toc.InlineValue(c, codecIDs[pickIndex(len(codecIDs), first)]); ok && len(v) >= 1 {
		sig.Codec = v[0]
	}
	if ids := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamSPS), start, stop); len(ids) > 0 {
		_, size, _ := toc.ContentOffset(c, ids[pickIndex(len(ids), first)])
		sig.SPSSize = size
	}
	if ids := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamPPS), start, stop); len(ids) > 0 {
		_, size, _ := toc.ContentOffset(c, ids[pickIndex(len(ids), first)])
		sig.PPSSize = size
	}
	return sig, true
}

func audioSigFor(c *toc.Columns, start, stop uint32, first bool) (audioSig, bool) {
	codecIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleCodecAudio), start, stop)
	if len(codecIDs) == 0 {
		return audioSig{}, false
	}
	var sig audioSig
	if v, ok := toc.InlineValue(c, codecIDs[pickIndex(len(codecIDs), first)]); ok && len(v) >= 1 {
		sig.Codec = v[0]
	}
	if ids := toc.InRange(toc.ScanByRole(c, mediatree.RoleParamAudioConfig), start, stop); len(ids) > 0 {
		_, size, _ := toc.ContentOffset(c, ids[pickIndex(len(ids), first)])
		sig.ASCSize = size
	}
	return sig, true
}

// configChanged reports whether the active video or audio config differs
// between the end of the previous record and the start of next, for
// channel — the condition ADR-019 requires a #EXT-X-DISCONTINUITY for.
func configChanged(prev, next *toc.Columns, channel uint16) bool {
	prevStart, prevStop, ok := channelSubtree(prev, channel)
	if !ok {
		return false
	}
	nextStart, nextStop, ok := channelSubtree(next, channel)
	if !ok {
		return false
	}

	prevVideo, prevHasVideo := videoSigFor(prev, prevStart, prevStop, false)
	nextVideo, nextHasVideo := videoSigFor(next, nextStart, nextStop, true)
	if prevHasVideo != nextHasVideo || (prevHasVideo && prevVideo != nextVideo) {
		return true
	}

	prevAudio, prevHasAudio := audioSigFor(prev, prevStart, prevStop, false)
	nextAudio, nextHasAudio := audioSigFor(next, nextStart, nextStop, true)
	if prevHasAudio != nextHasAudio || (prevHasAudio && prevAudio != nextAudio) {
		return true
	}

	return false
}
