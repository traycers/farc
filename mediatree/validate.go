package mediatree

import "fmt"

// Validate checks the structural invariants required of a Content tree —
// the checklist from docs/docs/archive/08-array-trees.md §13, applied to the
// parent/sibling pair. This doubles as corruption detection for a tree
// loaded from disk (a violation cannot arise from correct appends, only from
// bit rot or a bug).
//
// Because parent[i]<=id and sibling[i]<=id are checked first and enforced
// before anything else runs, the reachability check below is a single
// forward pass, not an iterate-to-fixpoint loop: by the time node i is
// visited, parent[i] (which is <= i) has already been visited. The general
// "propagate until no change" pattern from 08-array-trees.md §6.1 is only
// needed when that ordering isn't already known-good, which is not the case
// once the checks above have passed.
func Validate(elems []Element) error {
	n := uint32(len(elems))
	if n == 0 {
		return fmt.Errorf("mediatree: empty tree")
	}

	rootCount := 0
	var root uint32
	siblingClaimedBy := make(map[uint32]uint32, n) // sibling target id -> claimant id

	for i := uint32(0); i < n; i++ {
		e := elems[i]
		if e.Parent >= n {
			return fmt.Errorf("mediatree: node %d: parent %d out of range [0,%d)", i, e.Parent, n)
		}
		if e.Sibling >= n {
			return fmt.Errorf("mediatree: node %d: sibling %d out of range [0,%d)", i, e.Sibling, n)
		}
		if e.Parent > i {
			return fmt.Errorf("mediatree: node %d: parent %d violates parent<=id", i, e.Parent)
		}
		if e.Sibling > i {
			return fmt.Errorf("mediatree: node %d: sibling %d violates sibling<=id", i, e.Sibling)
		}
		if e.Parent == i {
			rootCount++
			root = i
		}
		if e.Sibling != i {
			if claimant, exists := siblingClaimedBy[e.Sibling]; exists {
				return fmt.Errorf("mediatree: nodes %d and %d both claim node %d as their left sibling", claimant, i, e.Sibling)
			}
			siblingClaimedBy[e.Sibling] = i
			if elems[e.Sibling].Parent != e.Parent {
				return fmt.Errorf("mediatree: node %d: sibling %d belongs to a different parent (%d vs %d)",
					i, e.Sibling, elems[e.Sibling].Parent, e.Parent)
			}
		}
		if !e.Type.Valid() {
			return fmt.Errorf("mediatree: node %d: invalid type code %d", i, e.Type)
		}
		if fixed, ok := e.Type.FixedSize(); ok && len(e.Value) != fixed {
			return fmt.Errorf("mediatree: node %d: type %s requires value of %d bytes, got %d", i, e.Type, fixed, len(e.Value))
		}
	}

	if rootCount != 1 {
		return fmt.Errorf("mediatree: expected exactly 1 self-referencing root, found %d", rootCount)
	}

	reached := make([]bool, n)
	reached[root] = true
	for i := uint32(0); i < n; i++ {
		if i == root {
			continue
		}
		if reached[elems[i].Parent] {
			reached[i] = true
		}
	}
	for i := uint32(0); i < n; i++ {
		if !reached[i] {
			return fmt.Errorf("mediatree: node %d is not reachable from root %d", i, root)
		}
	}
	return nil
}
