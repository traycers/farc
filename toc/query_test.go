package toc

import (
	"reflect"
	"testing"

	"github.com/traycers/farc/mediatree"
)

func builtSample(t *testing.T) *Columns {
	t.Helper()
	elems := sampleTree()
	cols, err := Build(elems, zeroOffsets(len(elems)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cols
}

// TestSubtreeRangeMatchesDocTable checks against the exact worked table in
// docs/docs/archive/08-array-trees.md §8.4.
func TestSubtreeRangeMatchesDocTable(t *testing.T) {
	cols := builtSample(t)
	cases := []struct {
		pos        uint32
		start, end uint32
	}{
		{0, 0, 13}, // root
		{2, 2, 9},  // channel(1)
		{9, 9, 13}, // channel(2)
		{3, 3, 9},  // streams (under channel 1)
		{4, 4, 7},  // stream(0)
	}
	for _, c := range cases {
		s, e := SubtreeRange(cols, c.pos)
		if s != c.start || e != c.end {
			t.Errorf("SubtreeRange(%d) = [%d,%d), want [%d,%d)", c.pos, s, e, c.start, c.end)
		}
	}
}

func TestChildren(t *testing.T) {
	cols := builtSample(t)
	if got := Children(cols, 0); !reflect.DeepEqual(got, []uint32{1}) {
		t.Errorf("Children(root) = %v, want [1]", got)
	}
	if got := Children(cols, 2); !reflect.DeepEqual(got, []uint32{3}) {
		t.Errorf("Children(channel(1)) = %v, want [3]", got)
	}
	if got := Children(cols, 3); !reflect.DeepEqual(got, []uint32{4, 7}) {
		t.Errorf("Children(streams) = %v, want [4,7]", got)
	}
	if got := Children(cols, 5); got != nil {
		t.Errorf("Children(leaf) = %v, want nil", got)
	}
}

func TestIsAncestor(t *testing.T) {
	cols := builtSample(t)
	if !IsAncestor(cols, 2, 4) { // channel(1)@2 ancestor of stream(0)@4
		t.Error("expected position 2 to be an ancestor of position 4")
	}
	if IsAncestor(cols, 9, 4) { // channel(2)@9 NOT ancestor of position4
		t.Error("expected position 9 to NOT be an ancestor of position 4")
	}
	if !IsAncestor(cols, 0, 12) { // root ancestor of everything
		t.Error("expected root to be an ancestor of position 12")
	}
	if !IsAncestor(cols, 5, 5) { // a node is its own ancestor
		t.Error("expected a node to be its own ancestor")
	}
}

func TestScanByRole(t *testing.T) {
	cols := builtSample(t)
	got := ScanByRole(cols, mediatree.RoleVideo)
	want := []uint32{5, 8, 12}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanByRole(video) = %v, want %v", got, want)
	}
}

// TestChannelNode checks against the same sampleTree()/doc-table positions
// TestSubtreeRangeMatchesDocTable uses: channel(1) sits at position 2,
// channel(2) at position 9.
func TestChannelNode(t *testing.T) {
	cols := builtSample(t)
	if id, ok := ChannelNode(cols, 1); !ok || id != 2 {
		t.Errorf("ChannelNode(1) = (%d, %v), want (2, true)", id, ok)
	}
	if id, ok := ChannelNode(cols, 2); !ok || id != 9 {
		t.Errorf("ChannelNode(2) = (%d, %v), want (9, true)", id, ok)
	}
	if _, ok := ChannelNode(cols, 3); ok {
		t.Error("ChannelNode(3) = ok, want not found (no such channel)")
	}
}

func TestChildByRole(t *testing.T) {
	cols := builtSample(t)
	// channel(1)@2's direct child is its own "streams" node @3 (see
	// TestSubtreeRangeMatchesDocTable's table).
	if id, ok := ChildByRole(cols, 2, mediatree.RoleStreams); !ok || id != 3 {
		t.Errorf("ChildByRole(channel(1), RoleStreams) = (%d, %v), want (3, true)", id, ok)
	}
	// channel(1) has no *direct* Video child -- Video is nested two levels
	// deeper, under streams -> stream.
	if _, ok := ChildByRole(cols, 2, mediatree.RoleVideo); ok {
		t.Error("ChildByRole(channel(1), RoleVideo) = ok, want not found (not a direct child)")
	}
}

func TestChannelSubtreeRange(t *testing.T) {
	cols := builtSample(t)
	if start, end, ok := ChannelSubtreeRange(cols, 1); !ok || start != 2 || end != 9 {
		t.Errorf("ChannelSubtreeRange(1) = (%d,%d,%v), want (2,9,true)", start, end, ok)
	}
	if start, end, ok := ChannelSubtreeRange(cols, 2); !ok || start != 9 || end != 13 {
		t.Errorf("ChannelSubtreeRange(2) = (%d,%d,%v), want (9,13,true)", start, end, ok)
	}
	if _, _, ok := ChannelSubtreeRange(cols, 3); ok {
		t.Error("ChannelSubtreeRange(3) = ok, want not found (no such channel)")
	}
}

func TestCoveringSubtreeRoot(t *testing.T) {
	cols := builtSample(t)
	// positions 5 and 8 are the two "video" nodes under channel(1)'s two
	// streams; their minimal covering subtree is "streams" under channel(1),
	// position 3 (old id 4) — see hand-derivation in the phase-3 report.
	got := CoveringSubtreeRoot(cols, []uint32{5, 8})
	if got != 3 {
		t.Errorf("CoveringSubtreeRoot([5,8]) = %d, want 3", got)
	}
	// LCA of a single-element set is the element itself.
	if got := CoveringSubtreeRoot(cols, []uint32{5}); got != 5 {
		t.Errorf("CoveringSubtreeRoot([5]) = %d, want 5", got)
	}
}

func TestTimeRangeFiltersSortedIDs(t *testing.T) {
	cols := &Columns{
		N:             5,
		Type:          make([]mediatree.NodeType, 5),
		Role:          []mediatree.Role{mediatree.RoleFrameTimeVideo, mediatree.RoleFrameTimeVideo, mediatree.RoleFrameTimeVideo, mediatree.RoleFrameTimeVideo, mediatree.RoleFrameTimeVideo},
		Parent:        make([]uint32, 5),
		Sibling:       make([]uint32, 5),
		ValueOrOffset: []uint64{100, 200, 300, 400, 500},
		Size:          make([]uint64, 5),
	}
	ids := ScanByRole(cols, mediatree.RoleFrameTimeVideo)
	got := TimeRange(cols, ids, 150, 350)
	want := []uint32{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TimeRange = %v, want %v", got, want)
	}
	if got := TimeRange(cols, ids, 1000, 2000); got != nil {
		t.Errorf("TimeRange out of range = %v, want nil", got)
	}
}

func TestInlineValueAndContentOffset(t *testing.T) {
	cols := &Columns{
		N:             2,
		Type:          []mediatree.NodeType{mediatree.TypeUint8, mediatree.TypeBytes},
		Role:          make([]mediatree.Role, 2),
		Parent:        make([]uint32, 2),
		Sibling:       make([]uint32, 2),
		ValueOrOffset: []uint64{7, 1024},
		Size:          []uint64{0, 42},
	}
	v, ok := InlineValue(cols, 0)
	if !ok || len(v) != 1 || v[0] != 7 {
		t.Errorf("InlineValue(0) = %v,%v, want [7],true", v, ok)
	}
	if _, ok := InlineValue(cols, 1); ok {
		t.Error("InlineValue on a bytes-typed node should report ok=false")
	}
	off, size, ok := ContentOffset(cols, 1)
	if !ok || off != 1024 || size != 42 {
		t.Errorf("ContentOffset(1) = %d,%d,%v, want 1024,42,true", off, size, ok)
	}
	if _, _, ok := ContentOffset(cols, 0); ok {
		t.Error("ContentOffset on a fixed-width node should report ok=false")
	}
}
