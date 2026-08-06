package toc

import (
	"encoding/binary"
	"reflect"
	"testing"

	"traycers/farc/mediatree"
)

// sampleTree reproduces the 13-node worked example from
// docs/docs/archive/08-array-trees.md §2, identical to the one in
// mediatree's own tests.
func sampleTree() []mediatree.Element {
	u32 := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		return b
	}
	return []mediatree.Element{
		{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot, Parent: 0, Sibling: 0},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleChannels, Parent: 0, Sibling: 1},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Parent: 1, Sibling: 2, Value: u32(1)},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Parent: 1, Sibling: 2, Value: u32(2)},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleStreams, Parent: 2, Sibling: 4},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleStreams, Parent: 3, Sibling: 5},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleStream, Parent: 4, Sibling: 6, Value: u32(0)},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleStream, Parent: 5, Sibling: 7, Value: u32(0)},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleVideo, Parent: 6, Sibling: 8},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleStream, Parent: 4, Sibling: 6, Value: u32(1)},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleAudio, Parent: 6, Sibling: 8},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleVideo, Parent: 9, Sibling: 11},
		{Type: mediatree.TypeVoid, Role: mediatree.RoleVideo, Parent: 7, Sibling: 12},
	}
}

func zeroOffsets(n int) []uint64 { return make([]uint64, n) } // no bytes/string values in sampleTree

// TestBuildMatchesDocWorkedExample checks Build's output against the exact
// new2old/parent'/sibling' vectors given in docs/docs/archive/
// 08-array-trees.md §8.3.
func TestBuildMatchesDocWorkedExample(t *testing.T) {
	elems := sampleTree()
	cols, err := Build(elems, zeroOffsets(len(elems)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantParent := []uint32{0, 0, 1, 2, 3, 4, 4, 3, 7, 1, 9, 10, 11}
	wantSibling := []uint32{0, 1, 2, 3, 4, 5, 5, 4, 8, 2, 10, 11, 12}
	wantNew2Old := []uint32{0, 1, 2, 4, 6, 8, 10, 9, 11, 3, 5, 7, 12}

	if !reflect.DeepEqual(cols.Parent, wantParent) {
		t.Errorf("Parent' = %v, want %v", cols.Parent, wantParent)
	}
	if !reflect.DeepEqual(cols.Sibling, wantSibling) {
		t.Errorf("Sibling' = %v, want %v", cols.Sibling, wantSibling)
	}

	// Recover new2old from Build's internal pos via role/value identity
	// (roles are unique enough on this sample tree using node identity
	// through the original Role+Value pairing) — simplest robust check:
	// old2new[old] should equal position where new2old side matches. We
	// instead verify by reconstructing: for each new position k, the
	// original element at new2old[k] must equal what's stored at cols[k].
	for k, oldID := range wantNew2Old {
		want := elems[oldID]
		if cols.Type[k] != want.Type || cols.Role[k] != want.Role {
			t.Errorf("position %d: type/role = %v/%v, want %v/%v (old id %d)", k, cols.Type[k], cols.Role[k], want.Type, want.Role, oldID)
		}
	}
}

func TestBuildInlineValuesRoundTrip(t *testing.T) {
	elems := sampleTree()
	cols, err := Build(elems, zeroOffsets(len(elems)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Old id 2 (channel(1), value=uint32(1)) ends up at new position 2
	// per the doc's worked example (new2old[2]==2).
	v, ok := InlineValue(cols, 2)
	if !ok {
		t.Fatalf("InlineValue(2): fixed-width value expected")
	}
	if binary.LittleEndian.Uint32(v) != 1 {
		t.Errorf("InlineValue(2) = %v, want channel number 1", v)
	}
	// Old id 3 (channel(2), value=uint32(2)) ends up at new position 9.
	v, ok = InlineValue(cols, 9)
	if !ok || binary.LittleEndian.Uint32(v) != 2 {
		t.Errorf("InlineValue(9) = %v,%v, want channel number 2", v, ok)
	}
}

func TestBuildEmptyTree(t *testing.T) {
	_, err := Build(nil, nil)
	if err == nil {
		t.Fatal("expected error building TOC for empty tree")
	}
}

func TestEncodeDecodeRoundTripOnBuiltColumns(t *testing.T) {
	elems := sampleTree()
	cols, err := Build(elems, zeroOffsets(len(elems)))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	buf, err := Encode(cols)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got.Parent, cols.Parent) || !reflect.DeepEqual(got.Sibling, cols.Sibling) ||
		!reflect.DeepEqual(got.Type, cols.Type) || !reflect.DeepEqual(got.Role, cols.Role) ||
		!reflect.DeepEqual(got.ValueOrOffset, cols.ValueOrOffset) || !reflect.DeepEqual(got.Size, cols.Size) {
		t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, cols)
	}
}
