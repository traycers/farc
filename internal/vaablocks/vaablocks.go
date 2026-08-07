// Package vaablocks computes vaa-blocks from a fblock's already-decoded TOC
// (internal/msmclient delivers the raw bytes, this package calls toc.Decode
// and walks the result) -- internal/msmd's only consumer. A vaa-block is a
// contiguous-in-time run of one channel's VIDEO frames within a single
// fblock; a gap of at least GapThresholdNS between two consecutive frames
// starts a new one, even though both sides are still the same channel in
// the same fblock. Audio is never grouped into vaa-blocks (msm's own
// requirement) -- only mediatree.RoleFrameTimeVideo nodes are scanned.
//
// A vaa-block never spans two fblocks: models.vaa_block.Id (temp/msm/
// openapi.yaml) is keyed by a single fnum, and this package only ever sees
// one fblock's TOC at a time anyway.
package vaablocks

import (
	"encoding/binary"
	"fmt"

	"traycers/farc/mediatree"
	"traycers/farc/toc"
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

// findChannelNode finds the RoleChannel node whose inline uint32 value is
// channel -- duplicated from internal/api/query.go's own unexported helper
// of the same name/shape (not importable across packages, and small enough
// not to warrant promoting to a shared package of its own).
func findChannelNode(c *toc.Columns, channel uint16) (uint32, bool) {
	for _, id := range toc.ScanByRole(c, mediatree.RoleChannel) {
		v, ok := toc.InlineValue(c, id)
		if ok && len(v) == 4 && binary.LittleEndian.Uint32(v) == uint32(channel) {
			return id, true
		}
	}
	return 0, false
}

// findChildByRole scans parentID's own subtree for a direct child with the
// given role -- duplicated from internal/api/query.go's own helper.
func findChildByRole(c *toc.Columns, parentID uint32, role mediatree.Role) (uint32, bool) {
	_, end := toc.SubtreeRange(c, parentID)
	for i := parentID + 1; i < end; i++ {
		if c.Parent[i] == parentID && c.Role[i] == role {
			return i, true
		}
	}
	return 0, false
}

// videoFrameSpan returns the content byte offset/size of the video frame
// owning frameTimeID (i.e. its RoleFrameDataVideo sibling under the same
// RoleFrameVideo parent).
func videoFrameSpan(c *toc.Columns, frameTimeID uint32) (offset, size uint64, err error) {
	frameID := c.Parent[frameTimeID]
	dataID, ok := findChildByRole(c, frameID, mediatree.RoleFrameDataVideo)
	if !ok {
		return 0, 0, fmt.Errorf("vaablocks: frame %d has no video data child", frameID)
	}
	offset, size, ok = toc.ContentOffset(c, dataID)
	if !ok {
		return 0, 0, fmt.Errorf("vaablocks: frame %d data node is not variable-width", frameID)
	}
	return offset, size, nil
}

// configAndStreamOf walks up from a frame_time(video) node to the
// RoleConfigVideo node governing it and the RoleStream node's inline stream
// number -- the tree shape internal/fcontainer/filler.go's AddStreamParams
// builds: frame_time -> frame(video) -> frames(video) -> config(video) ->
// configs(video) -> video -> stream(uint32 inline value).
func configAndStreamOf(c *toc.Columns, frameTimeID uint32) (configID uint32, streamID uint16, err error) {
	frameID := c.Parent[frameTimeID]
	framesID := c.Parent[frameID]
	configID = c.Parent[framesID]
	configsID := c.Parent[configID]
	kindID := c.Parent[configsID]
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

// Compute returns channel's video vaa-blocks from c, in increasing-time
// order -- nil if channel isn't present in c or has no video frames.
func Compute(c *toc.Columns, channel uint16) ([]Block, error) {
	channelNodeID, ok := findChannelNode(c, channel)
	if !ok {
		return nil, nil
	}
	start, end := toc.SubtreeRange(c, channelNodeID)
	timeIDs := toc.InRange(toc.ScanByRole(c, mediatree.RoleFrameTimeVideo), start, end)
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
		firstOffset, _, err := videoFrameSpan(c, run[0])
		if err != nil {
			return nil, err
		}
		lastOffset, lastSize, err := videoFrameSpan(c, run[len(run)-1])
		if err != nil {
			return nil, err
		}
		configID, streamID, err := configAndStreamOf(c, run[0])
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
