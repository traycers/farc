package mediatree

import "testing"

// TestDecodeContentPartial_CleanStop verifies DecodeContentPartial decodes
// every element when the buffer contains nothing but valid elements —
// matching DecodeContentWithOffsets exactly (docs/docs/archive/
// adr/017-periodic-fchunk-flush.md's crash-recovery algorithm only differs
// from the normal decode path when the buffer is truncated).
func TestDecodeContentPartial_CleanStop(t *testing.T) {
	elems := sampleTree()
	buf := EncodeContent(elems)

	wantElems, wantOffsets, err := DecodeContentWithOffsets(buf)
	if err != nil {
		t.Fatalf("DecodeContentWithOffsets: %v", err)
	}

	gotElems, gotOffsets := DecodeContentPartial(buf)
	if len(gotElems) != len(wantElems) {
		t.Fatalf("DecodeContentPartial decoded %d elements, want %d", len(gotElems), len(wantElems))
	}
	for i := range wantElems {
		if gotElems[i].Type != wantElems[i].Type || gotElems[i].Role != wantElems[i].Role ||
			gotElems[i].Parent != wantElems[i].Parent || gotElems[i].Sibling != wantElems[i].Sibling ||
			string(gotElems[i].Value) != string(wantElems[i].Value) {
			t.Errorf("element %d mismatch: got %+v, want %+v", i, gotElems[i], wantElems[i])
		}
		if gotOffsets[i] != wantOffsets[i] {
			t.Errorf("offset %d = %d, want %d", i, gotOffsets[i], wantOffsets[i])
		}
	}
}

// TestDecodeContentPartial_StopsOnTruncatedElement simulates a crash
// recovery scan of raw content bytes cut off mid-element (e.g. the trailer
// happened to land inside what would have been the next element's value) —
// DecodeContentPartial must return everything decoded cleanly before the
// cut, never an error, discarding only the one dangling partial element.
func TestDecodeContentPartial_StopsOnTruncatedElement(t *testing.T) {
	elems := sampleTree()
	full := EncodeContent(elems)

	// Truncate mid-way through the last element's header (19-byte fixed
	// portion) -- not enough bytes to decode one more full element.
	cut := len(full) - 5
	buf := full[:cut]

	gotElems, gotOffsets := DecodeContentPartial(buf)
	if len(gotElems) != len(elems)-1 {
		t.Fatalf("DecodeContentPartial decoded %d elements, want %d (all but the truncated last one)", len(gotElems), len(elems)-1)
	}
	if len(gotOffsets) != len(gotElems) {
		t.Fatalf("len(offsets) = %d, want %d", len(gotOffsets), len(gotElems))
	}
	for i := range gotElems {
		if gotElems[i].Type != elems[i].Type || gotElems[i].Role != elems[i].Role {
			t.Errorf("element %d mismatch: got %+v, want %+v", i, gotElems[i], elems[i])
		}
	}
}

// TestDecodeContentPartial_EmptyBuffer covers the crash-before-any-content
// case (no trigger ever landed a byte of real content).
func TestDecodeContentPartial_EmptyBuffer(t *testing.T) {
	gotElems, gotOffsets := DecodeContentPartial(nil)
	if gotElems != nil || gotOffsets != nil {
		t.Fatalf("DecodeContentPartial(nil) = (%v, %v), want (nil, nil)", gotElems, gotOffsets)
	}
}
