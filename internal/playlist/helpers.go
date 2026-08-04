package playlist

import (
	"encoding/binary"

	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// findChannelNode finds the RoleChannel node whose inline uint32 value is
// channel, within c's own row-index space. Reimplements
// internal/api/query.go's unexported helper of the same name locally, since
// it isn't reachable from this package.
func findChannelNode(c *toc.Columns, channel uint16) (uint32, bool) {
	for _, id := range toc.ScanByRole(c, mediatree.RoleChannel) {
		v, ok := toc.InlineValue(c, id)
		if ok && len(v) == 4 && binary.LittleEndian.Uint32(v) == uint32(channel) {
			return id, true
		}
	}
	return 0, false
}

// channelSubtree resolves channel's node and returns its [start,stop) range
// (docs/docs/archive/06-toc-format.md §6, toc.SubtreeRange).
func channelSubtree(c *toc.Columns, channel uint16) (start, stop uint32, ok bool) {
	id, found := findChannelNode(c, channel)
	if !found {
		return 0, 0, false
	}
	start, stop = toc.SubtreeRange(c, id)
	return start, stop, true
}

// findChildByRole scans parentID's own subtree range for a direct child
// (parent == parentID) with the given role — toc.Columns has no ready-made
// equivalent of mediatree.FindChildByRole (that helper works on the
// pre-reorder []Element representation, not post-reorder Columns).
// Reimplements internal/api/query.go's unexported helper of the same name.
func findChildByRole(c *toc.Columns, parentID uint32, role mediatree.Role) (uint32, bool) {
	_, end := toc.SubtreeRange(c, parentID)
	for i := parentID + 1; i < end; i++ {
		if c.Parent[i] == parentID && c.Role[i] == role {
			return i, true
		}
	}
	return 0, false
}
