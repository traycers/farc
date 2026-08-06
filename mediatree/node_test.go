package mediatree

import (
	"encoding/binary"
	"testing"
)

// sampleTree builds the 13-node worked example from
// docs/docs/archive/08-array-trees.md §2 (two channels; channel 1 has two
// streams — video+audio on stream 0, video on stream 1; channel 2 has one
// stream with video), using only the roles present in that simplified
// fragment (root/channels/channel/streams/stream/video/audio).
func sampleTree() []Element {
	u32 := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		return b
	}
	return []Element{
		{Type: TypeVoid, Role: RoleRoot, Parent: 0, Sibling: 0},                     // 0
		{Type: TypeVoid, Role: RoleChannels, Parent: 0, Sibling: 1},                 // 1
		{Type: TypeUint32, Role: RoleChannel, Parent: 1, Sibling: 2, Value: u32(1)}, // 2 channel(1)
		{Type: TypeUint32, Role: RoleChannel, Parent: 1, Sibling: 2, Value: u32(2)}, // 3 channel(2)
		{Type: TypeVoid, Role: RoleStreams, Parent: 2, Sibling: 4},                  // 4
		{Type: TypeVoid, Role: RoleStreams, Parent: 3, Sibling: 5},                  // 5
		{Type: TypeUint32, Role: RoleStream, Parent: 4, Sibling: 6, Value: u32(0)},  // 6 stream(0)
		{Type: TypeUint32, Role: RoleStream, Parent: 5, Sibling: 7, Value: u32(0)},  // 7 stream(0)
		{Type: TypeVoid, Role: RoleVideo, Parent: 6, Sibling: 8},                    // 8
		{Type: TypeUint32, Role: RoleStream, Parent: 4, Sibling: 6, Value: u32(1)},  // 9 stream(1)
		{Type: TypeVoid, Role: RoleAudio, Parent: 6, Sibling: 8},                    // 10
		{Type: TypeVoid, Role: RoleVideo, Parent: 9, Sibling: 11},                   // 11
		{Type: TypeVoid, Role: RoleVideo, Parent: 7, Sibling: 12},                   // 12
	}
}

func TestSampleTreeRoundTripAndValidate(t *testing.T) {
	elems := sampleTree()
	buf := EncodeContent(elems)

	got, err := DecodeContent(buf)
	if err != nil {
		t.Fatalf("DecodeContent: %v", err)
	}
	if len(got) != len(elems) {
		t.Fatalf("decoded %d elements, want %d", len(got), len(elems))
	}
	for i := range elems {
		if got[i].Type != elems[i].Type || got[i].Role != elems[i].Role ||
			got[i].Parent != elems[i].Parent || got[i].Sibling != elems[i].Sibling ||
			string(got[i].Value) != string(elems[i].Value) {
			t.Errorf("element %d mismatch: got %+v, want %+v", i, got[i], elems[i])
		}
	}

	err = Validate(got)
	if err != nil {
		t.Fatalf("Validate() on the doc's own worked example: %v", err)
	}
}

func TestChildrenAndFindChildByRole(t *testing.T) {
	elems := sampleTree()
	kids := Children(elems, 4) // streams container under channel(1)
	if len(kids) != 2 || kids[0] != 6 || kids[1] != 9 {
		t.Fatalf("Children(4) = %v, want [6 9]", kids)
	}
	if _, ok := FindChildByRole(elems, 0, RoleChannels); !ok {
		t.Errorf("expected root to have a channels child")
	}
	if id, ok := FindChildByRole(elems, 6, RoleAudio); !ok || id != 10 {
		t.Errorf("FindChildByRole(6, audio) = (%d,%v), want (10,true)", id, ok)
	}
}

func TestValidateCatchesParentInvariantViolation(t *testing.T) {
	elems := sampleTree()
	elems[4].Parent = 5 // 5 > 4, violates parent<=id
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for parent > id")
	}
}

func TestValidateCatchesSiblingInvariantViolation(t *testing.T) {
	elems := sampleTree()
	elems[6].Sibling = 8 // 8 > 6, violates sibling<=id
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for sibling > id")
	}
}

func TestValidateCatchesSiblingWrongParent(t *testing.T) {
	elems := sampleTree()
	// Node 9's sibling is 6, but let's break it: point 9's sibling at 8,
	// whose parent (6) differs from 9's parent (4).
	elems[9].Sibling = 8
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for sibling belonging to a different parent")
	}
}

func TestValidateCatchesDuplicateSiblingClaim(t *testing.T) {
	elems := sampleTree()
	// Force two different nodes to claim the same left sibling.
	elems[3].Sibling = 2
	elems[5].Parent = 1
	elems[5].Sibling = 2 // node 5 now also claims node 2 as its left sibling
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for duplicate sibling claim")
	}
}

func TestValidateCatchesMultipleRoots(t *testing.T) {
	elems := sampleTree()
	elems[1].Parent = 1 // a second self-referencing "root"
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for a second root")
	}
}

func TestValidateCatchesTypeSizeMismatch(t *testing.T) {
	elems := sampleTree()
	elems[2].Value = []byte{1, 2, 3} // uint32 role given a 3-byte value
	if err := Validate(elems); err == nil {
		t.Fatal("expected error for wrong-size fixed-type value")
	}
}

func TestFindKeyframeWalksSiblingChain(t *testing.T) {
	fr := buildGOP() // frame nodes: 1 (I), 3 (P), 5 (P)
	kf, err := FindKeyframe(fr, 5)
	if err != nil {
		t.Fatalf("FindKeyframe: %v", err)
	}
	if kf != 1 {
		t.Fatalf("FindKeyframe(5) = %d, want 1 (the I frame)", kf)
	}
	// Asking from the I frame itself should return itself immediately.
	if kf, err := FindKeyframe(fr, 1); err != nil || kf != 1 {
		t.Fatalf("FindKeyframe(1) = (%d,%v), want (1,nil)", kf, err)
	}
}

// buildGOP builds: 0 frames(void) -> 1 frame(I,kind child=2) -> 3 frame(P,kind=4) -> 5 frame(P,kind=6)
func buildGOP() []Element {
	return []Element{
		{Type: TypeVoid, Role: RoleFramesVideo, Parent: 0, Sibling: 0},                           // 0 frames container (self-parent for this standalone test)
		{Type: TypeVoid, Role: RoleFrameVideo, Parent: 0, Sibling: 1},                            // 1 frame (I), first child of 0
		{Type: TypeUint8, Role: RoleFrameKind, Parent: 1, Sibling: 2, Value: []byte{FrameKindI}}, // 2
		{Type: TypeVoid, Role: RoleFrameVideo, Parent: 0, Sibling: 1},                            // 3 frame (P), sibling=1 (previous frame)
		{Type: TypeUint8, Role: RoleFrameKind, Parent: 3, Sibling: 4, Value: []byte{FrameKindP}}, // 4
		{Type: TypeVoid, Role: RoleFrameVideo, Parent: 0, Sibling: 3},                            // 5 frame (P), sibling=3
		{Type: TypeUint8, Role: RoleFrameKind, Parent: 5, Sibling: 6, Value: []byte{FrameKindP}}, // 6
	}
}
