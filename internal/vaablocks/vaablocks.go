// Package vaablocks computes vaa-blocks from a fblock's already-decoded TOC
// (internal/msmclient delivers the raw bytes, this package calls toc.Decode
// and walks the result) -- internal/msmd's only consumer. A vaa-block is a
// contiguous-in-time run of one channel's frames of a given StreamKind
// within a single fblock; a gap of at least GapThresholdNS between two
// consecutive frames starts a new one, even though both sides are still the
// same channel in the same fblock. Video and audio each get their own,
// entirely independent vaa-block timeline (.scratch/msm-integration/issues/
// 01-audio-vaa-blocks.md) -- Compute takes a StreamKind and scans only that
// kind's frame_time role.
//
// A vaa-block never spans two fblocks: models.vaa_block.Id (temp/msm/
// openapi.yaml) is keyed by a single fnum, and this package only ever sees
// one fblock's TOC at a time anyway.
package vaablocks

import (
	"encoding/binary"
	"fmt"

	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// GapThresholdNS is msm's vaa-block boundary rule (ns): 2 seconds.
const GapThresholdNS = uint64(2_000_000_000)

// Block is one computed vaa-block.
type Block struct {
	Channel uint16
	Begin   uint64 // ns, first frame's time
	End     uint64 // ns, last frame's time

	// ConfigID/StreamID identify the config version (StreamConfigs'
	// StreamConfig.ConfigID) and stream number governing the run's first
	// frame -- msm's params_id/stream_id for this vaa-block. A params
	// change mid-run (rare) is not specially handled: the whole run is
	// attributed to its first frame's config.
	ConfigID uint32
	StreamID uint16

	// Offset/Size describe the byte span in the fblock's Content section
	// (toc.ContentOffset's own frame of reference -- the same one
	// GET .../fcontainers/{uuid}?ranges=off:len already reads against) from
	// the first frame's data to the last frame's data. Frames of other
	// kinds/channels interleaved in time (e.g. this channel's own audio)
	// can fall inside that span even though they aren't part of the
	// vaa-block themselves -- msm's spec doesn't ask for a byte-exact,
	// video-only span, just "the block's" offset/size, and a span that
	// fully contains every one of this run's video frames is the most
	// useful reading of that without further guidance.
	Offset uint64
	Size   uint64
}

// Channels returns every channel number present in c (mediatree.RoleChannel
// nodes), in no particular order.
func Channels(c *toc.Columns) []uint16 {
	ids := toc.ScanByRole(c, mediatree.RoleChannel)
	out := make([]uint16, 0, len(ids))
	for _, id := range ids {
		v, ok := toc.InlineValue(c, id)
		if ok && len(v) == 4 {
			out = append(out, uint16(binary.LittleEndian.Uint32(v)))
		}
	}
	return out
}

// frameTimeRole/frameDataRole/configRole/configsRole/kindRole return kind's
// role codes for the tree shape internal/fcontainer/filler.go's
// AddStreamParams builds: frame_time -> frame -> frames -> config ->
// configs -> [video|audio] -> stream(uint32 inline value). Video and audio
// each have their own role codes for these structurally-identical
// positions (mediatree.Role's own doc comment).
func frameTimeRole(kind StreamKind) mediatree.Role {
	if kind == KindAudio {
		return mediatree.RoleFrameTimeAudio
	}
	return mediatree.RoleFrameTimeVideo
}

func frameDataRole(kind StreamKind) mediatree.Role {
	if kind == KindAudio {
		return mediatree.RoleFrameDataAudio
	}
	return mediatree.RoleFrameDataVideo
}

func kindRole(kind StreamKind) mediatree.Role {
	if kind == KindAudio {
		return mediatree.RoleAudio
	}
	return mediatree.RoleVideo
}

// frameSpan returns the content byte offset/size of the frame owning
// frameTimeID (i.e. its frame-data role sibling under the same frame
// parent).
func frameSpan(c *toc.Columns, frameTimeID uint32, kind StreamKind) (offset, size uint64, err error) {
	frameID := c.Parent[frameTimeID]
	dataID, ok := toc.ChildByRole(c, frameID, frameDataRole(kind))
	if !ok {
		return 0, 0, fmt.Errorf("vaablocks: frame %d has no %v data child", frameID, kind)
	}
	offset, size, ok = toc.ContentOffset(c, dataID)
	if !ok {
		return 0, 0, fmt.Errorf("vaablocks: frame %d data node is not variable-width", frameID)
	}
	return offset, size, nil
}

// configAndStreamOf walks up from a frame_time node to the config node
// governing it and the RoleStream node's inline stream number.
func configAndStreamOf(c *toc.Columns, frameTimeID uint32, kind StreamKind) (configID uint32, streamID uint16, err error) {
	frameID := c.Parent[frameTimeID]
	framesID := c.Parent[frameID]
	configID = c.Parent[framesID]
	configsID := c.Parent[configID]
	kindID := c.Parent[configsID]
	if c.Role[kindID] != kindRole(kind) {
		return 0, 0, fmt.Errorf("vaablocks: frame %d: expected a %v ancestor, got role %s", frameID, kind, c.Role[kindID])
	}
	streamNodeID := c.Parent[kindID]
	if c.Role[streamNodeID] != mediatree.RoleStream {
		return 0, 0, fmt.Errorf("vaablocks: frame %d: expected a stream ancestor, got role %s", frameID, c.Role[streamNodeID])
	}
	v, ok := toc.InlineValue(c, streamNodeID)
	if !ok || len(v) != 4 {
		return 0, 0, fmt.Errorf("vaablocks: frame %d: stream node %d has no inline uint32 value", frameID, streamNodeID)
	}
	return configID, uint16(binary.LittleEndian.Uint32(v)), nil
}

// Compute returns channel's kind vaa-blocks from c, in increasing-time
// order -- nil if channel isn't present in c or has no frames of kind.
// Video and audio are computed entirely independently: each has its own
// gap-splitting timeline, never merged or cross-referenced.
func Compute(c *toc.Columns, channel uint16, kind StreamKind) ([]Block, error) {
	start, end, ok := toc.ChannelSubtreeRange(c, channel)
	if !ok {
		return nil, nil
	}
	timeIDs := toc.InRange(toc.ScanByRole(c, frameTimeRole(kind)), start, end)
	if len(timeIDs) == 0 {
		return nil, nil
	}

	var blocks []Block
	runStart := 0
	for i := 1; i <= len(timeIDs); i++ {
		if i < len(timeIDs) && c.ValueOrOffset[timeIDs[i]]-c.ValueOrOffset[timeIDs[i-1]] < GapThresholdNS {
			continue
		}
		run := timeIDs[runStart:i]
		firstOffset, _, err := frameSpan(c, run[0], kind)
		if err != nil {
			return nil, err
		}
		lastOffset, lastSize, err := frameSpan(c, run[len(run)-1], kind)
		if err != nil {
			return nil, err
		}
		configID, streamID, err := configAndStreamOf(c, run[0], kind)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, Block{
			Channel:  channel,
			Begin:    c.ValueOrOffset[run[0]],
			End:      c.ValueOrOffset[run[len(run)-1]],
			Offset:   firstOffset,
			Size:     lastOffset + lastSize - firstOffset,
			ConfigID: configID,
			StreamID: streamID,
		})
		runStart = i
	}
	return blocks, nil
}
