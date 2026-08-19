package ingest

import (
	"testing"

	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/mediatree"
)

func countRoleElems(elems []mediatree.Element, role mediatree.Role) int {
	n := 0
	for _, e := range elems {
		if e.Role == role {
			n++
		}
	}
	return n
}

func TestFilterChannelElements_ExtractsOnlyThatChannelAsAStandaloneTree(t *testing.T) {
	f := fcontainer.New()
	cid1, err := f.AddStreamParams(1, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams(1): %v", err)
	}
	if err := f.AddFrames(cid1, []fcontainer.Frame{vframe(10, mediatree.FrameKindI)}); err != nil {
		t.Fatalf("AddFrames(1): %v", err)
	}
	cid2, err := f.AddStreamParams(2, 0, fcontainer.KindVideo, videoParams(0))
	if err != nil {
		t.Fatalf("AddStreamParams(2): %v", err)
	}
	if err := f.AddFrames(cid2, []fcontainer.Frame{vframe(20, mediatree.FrameKindI), vframe(30, mediatree.FrameKindP)}); err != nil {
		t.Fatalf("AddFrames(2): %v", err)
	}

	merged := f.Elements()

	got1 := filterChannelElements(merged, 1)
	if err := mediatree.Validate(got1); err != nil {
		t.Fatalf("filterChannelElements(1) invalid tree: %v", err)
	}
	if n := countRoleElems(got1, mediatree.RoleFrameVideo); n != 1 {
		t.Fatalf("channel 1: frame(video) count = %d, want 1", n)
	}
	if n := countRoleElems(got1, mediatree.RoleChannel); n != 1 {
		t.Fatalf("channel 1: channel node count = %d, want 1 (must not include channel 2)", n)
	}

	got2 := filterChannelElements(merged, 2)
	if err := mediatree.Validate(got2); err != nil {
		t.Fatalf("filterChannelElements(2) invalid tree: %v", err)
	}
	if n := countRoleElems(got2, mediatree.RoleFrameVideo); n != 2 {
		t.Fatalf("channel 2: frame(video) count = %d, want 2", n)
	}

	if got := filterChannelElements(merged, 99); got != nil {
		t.Fatalf("filterChannelElements(unknown channel) = %v, want nil", got)
	}
	if got := filterChannelElements(nil, 1); got != nil {
		t.Fatalf("filterChannelElements(nil) = %v, want nil", got)
	}
}
