package toc

import (
	"encoding/binary"
	"sort"

	"github.com/traycers/farc/mediatree"
)

// SubtreeRange returns [start, end) — the contiguous range of positions
// covered by the subtree rooted at position k (docs/docs/archive/
// 06-toc-format.md §6). Correct because in DFS preorder, any node encountered
// before parent[i] < k drops below k's own position, marking the first
// position outside the subtree opened at k.
func SubtreeRange(c *Columns, k uint32) (start, end uint32) {
	end = k + 1
	for end < c.N && c.Parent[end] >= k {
		end++
	}
	return k, end
}

// Children returns the ids of node parentID's direct children, in DFS
// preorder (docs/docs/archive/06-toc-format.md §5) — the toc.Columns
// analogue of mediatree.Children, which instead operates on the
// pre-reorder, creation-order []Element representation. Bounded to
// parentID's own SubtreeRange: DFS preorder guarantees Parent[i] < i for
// every i, so no id outside [parentID+1, end) can be a child of parentID
// (it would have to appear before parentID itself), and every actual
// child's whole subtree is nested inside that range by SubtreeRange's own
// definition — so the scan is both sound and complete.
func Children(c *Columns, parentID uint32) []uint32 {
	_, end := SubtreeRange(c, parentID)
	var kids []uint32
	for i := parentID + 1; i < end; i++ {
		if c.Parent[i] == parentID {
			kids = append(kids, i)
		}
	}
	return kids
}

// IsAncestor reports whether a is an ancestor of b (or a == b), walking the
// Parent chain from b (docs/docs/archive/06-toc-format.md §6).
func IsAncestor(c *Columns, a, b uint32) bool {
	for i := b; ; i = c.Parent[i] {
		if i == a {
			return true
		}
		if c.Parent[i] == i { // reached root without matching a
			return false
		}
	}
}

// ScanByRole returns, in position order, every node whose Role is one of
// roles. Used e.g. to find the few `channel` nodes in a fcontainer (ADR-014:
// channel counts per fcontainer are small) or to filter frame_time nodes
// across both the video (19) and audio (32) codes for a cross-modality time
// query (docs/docs/archive/07-media-tree.md §3.1).
func ScanByRole(c *Columns, roles ...mediatree.Role) []uint32 {
	set := make(map[mediatree.Role]bool, len(roles))
	for _, r := range roles {
		set[r] = true
	}
	var out []uint32
	for i := uint32(0); i < c.N; i++ {
		if set[c.Role[i]] {
			out = append(out, i)
		}
	}
	return out
}

// InRange filters ids (assumed sorted ascending, as ScanByRole produces) to
// those within [lo, hi).
func InRange(ids []uint32, lo, hi uint32) []uint32 {
	var out []uint32
	for _, id := range ids {
		if id >= lo && id < hi {
			out = append(out, id)
		}
	}
	return out
}

// TimeRange filters a set of same-role node ids (assumed already in
// increasing-time order, as guaranteed for frame_time nodes within one
// subtree by creation order = decode order, docs/docs/archive/
// 06-toc-format.md §7) to those whose ValueOrOffset (interpreted as a
// uint64 Unix-ns timestamp) falls in [t1, t2].
func TimeRange(c *Columns, ids []uint32, t1, t2 uint64) []uint32 {
	lo := sort.Search(len(ids), func(k int) bool { return c.ValueOrOffset[ids[k]] >= t1 })
	hi := sort.Search(len(ids), func(k int) bool { return c.ValueOrOffset[ids[k]] > t2 })
	if lo >= hi {
		return nil
	}
	return ids[lo:hi]
}

// CoveringSubtreeRoot returns the position of the minimal subtree root
// covering every id in ids — the LCA of a whole set (docs/docs/archive/
// 08-array-trees.md §10.6), via a single backward counting pass: naive and
// O(n), which the docs recommend over binary lifting at farc's scale (§10.8).
func CoveringSubtreeRoot(c *Columns, ids []uint32) uint32 {
	n := c.N
	cnt := make([]int, n)
	for _, id := range ids {
		cnt[id]++
	}
	for i := int64(n) - 1; i >= 0; i-- {
		id := uint32(i)
		if c.Parent[id] != id {
			cnt[c.Parent[id]] += cnt[id]
		}
	}
	depth := computeDepth(c)
	want := len(ids)
	var best uint32
	bestDepth := int64(-1)
	for i := uint32(0); i < n; i++ {
		if cnt[i] == want && int64(depth[i]) > bestDepth {
			best = i
			bestDepth = int64(depth[i])
		}
	}
	return best
}

// computeDepth derives each node's depth from Parent, in a single forward
// pass exploiting Parent[i]<=i — depth is never stored on disk
// (docs/docs/archive/05-data-format.md §3, 08-array-trees.md §6.2).
func computeDepth(c *Columns) []uint32 {
	depth := make([]uint32, c.N)
	for i := uint32(0); i < c.N; i++ {
		if c.Parent[i] == i {
			continue
		}
		depth[i] = depth[c.Parent[i]] + 1
	}
	return depth
}

// InlineValue returns node i's value bytes for a fixed-width type (the
// inverse of the ValueOrOffset packing done at Build time). It is an error
// to call this for a variable-width type (bytes/string) — use Offset/Size
// instead to read from Content.
func InlineValue(c *Columns, i uint32) ([]byte, bool) {
	fixedSize, ok := c.Type[i].FixedSize()
	if !ok {
		return nil, false
	}
	return UnpackInline(c.ValueOrOffset[i], fixedSize), true
}

// ChannelNode finds the RoleChannel node whose inline uint32 value is
// channel, within c's own row-index space (ADR-014: channel counts per
// fcontainer are small, so a linear scan over ScanByRole's result is
// cheap). Was independently reimplemented in internal/api, internal/
// tocindex, internal/segment, and internal/playlist before being promoted
// here.
func ChannelNode(c *Columns, channel uint16) (uint32, bool) {
	for _, id := range ScanByRole(c, mediatree.RoleChannel) {
		v, ok := InlineValue(c, id)
		if ok && len(v) == 4 && binary.LittleEndian.Uint32(v) == uint32(channel) {
			return id, true
		}
	}
	return 0, false
}

// ChildByRole scans parentID's own subtree range for a direct child
// (Parent == parentID) with the given role — Columns has no ready-made
// equivalent of mediatree.FindChildByRole (that helper works on
// []Element, the pre-reorder write-time representation, not this
// post-reorder Columns).
func ChildByRole(c *Columns, parentID uint32, role mediatree.Role) (uint32, bool) {
	_, end := SubtreeRange(c, parentID)
	for i := parentID + 1; i < end; i++ {
		if c.Parent[i] == parentID && c.Role[i] == role {
			return i, true
		}
	}
	return 0, false
}

// ChannelSubtreeRange resolves channel's node via ChannelNode and returns
// its own SubtreeRange -- the common "find this channel, then look at
// everything under it" starting point shared by every caller that walks a
// single channel's frames/streams/configs.
func ChannelSubtreeRange(c *Columns, channel uint16) (start, end uint32, ok bool) {
	id, found := ChannelNode(c, channel)
	if !found {
		return 0, 0, false
	}
	start, end = SubtreeRange(c, id)
	return start, end, true
}

// ContentOffset returns the byte offset into Content where node i's value
// begins, for a variable-width type (bytes/string). ok is false for
// fixed-width types, whose value lives inline in ValueOrOffset instead.
func ContentOffset(c *Columns, i uint32) (offset uint64, size uint64, ok bool) {
	if c.Type[i].Variable() {
		return c.ValueOrOffset[i], c.Size[i], true
	}
	return 0, 0, false
}
