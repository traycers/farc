package ingest

import (
	"encoding/binary"

	"github.com/traycers/farc/mediatree"
)

// filterChannelElements extracts channel's own subtree from elems (a
// shared segment's full, possibly-multi-channel tree) and rebuilds it as a
// standalone root->channels->channel(...) tree, renumbered to a fresh
// dense 0-based id range in first-seen (= original scan) order --
// preserving the "Parent[i] < i" / "Sibling[i] <= i" invariants
// (mediatree.Validate, docs/docs/archive/08-array-trees.md) so every
// existing consumer of LiveSnapshot.Elements (internal/api/fblocktree.go's
// buildLiveNodes chief among them) keeps working completely unchanged,
// exactly as if this channel still had its own private Filler. Returns nil
// if elems is empty or channel has no node in it yet (not yet joined onto
// the current segment).
func filterChannelElements(elems []mediatree.Element, channel uint32) []mediatree.Element {
	if len(elems) == 0 {
		return nil
	}

	channelsID, ok := mediatree.FindChildByRole(elems, 0, mediatree.RoleChannels)
	if !ok {
		return nil
	}
	var channelID uint32
	found := false
	for _, id := range mediatree.Children(elems, channelsID) {
		if elems[id].Role == mediatree.RoleChannel && len(elems[id].Value) == 4 &&
			binary.LittleEndian.Uint32(elems[id].Value) == channel {
			channelID, found = id, true
			break
		}
	}
	if !found {
		return nil
	}

	// include[id] is true for channelID and every descendant of it --
	// safe as a single forward scan since a child's id is always greater
	// than its parent's (the tree's own append-only invariant).
	include := map[uint32]bool{channelID: true}
	for id := channelID + 1; id < uint32(len(elems)); id++ {
		if include[elems[id].Parent] {
			include[id] = true
		}
	}

	remap := make(map[uint32]uint32, len(include)+2)
	out := make([]mediatree.Element, 0, len(include)+2)

	newRoot := uint32(len(out))
	out = append(out, mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: newRoot, Sibling: newRoot})
	newChannels := uint32(len(out))
	out = append(out, mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleChannels, Parent: newRoot, Sibling: newChannels})

	channelElem := elems[channelID]
	channelElem.Parent = newChannels
	newChannelID := uint32(len(out))
	channelElem.Sibling = newChannelID // sole child of the synthetic channels container
	remap[channelID] = newChannelID
	out = append(out, channelElem)

	for id := channelID + 1; id < uint32(len(elems)); id++ {
		if !include[id] {
			continue
		}
		e := elems[id]
		newParent, ok := remap[e.Parent]
		if !ok {
			continue // unreachable given how include was built, but never trust an index into a mutated slice blindly
		}
		newID := uint32(len(out))
		newSibling := newID // default: first child under its parent (self-reference)
		if s, ok := remap[e.Sibling]; ok {
			newSibling = s
		}
		e.Parent = newParent
		e.Sibling = newSibling
		remap[id] = newID
		out = append(out, e)
	}
	return out
}
