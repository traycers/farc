package toc

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/traycers/farc/mediatree"
)

func TestComputeOffsetsAlignment(t *testing.T) {
	for _, n := range []uint32{0, 1, 7, 64, 65, 1000, 1_000_000} {
		offsets, total := columnOffsets(canonicalColumns, n)
		for _, off := range offsets {
			if off%Align != 0 {
				t.Errorf("n=%d: offset %d not %d-aligned", n, off, Align)
			}
		}
		// Unlike the old format, the last column is no longer a special
		// case — the whole section, including its end, is Align-aligned.
		if total%Align != 0 {
			t.Errorf("n=%d: total %d not %d-aligned", n, total, Align)
		}
	}
}

func TestPaddingCostBound(t *testing.T) {
	// docs/docs/archive/06-toc-format.md §3: at most 6*63=378 bytes of
	// per-column padding (6 boundaries now that Size is no longer exempt),
	// plus a fixed 40 bytes padding the 6-entry (24-byte) column directory
	// up to the next Align boundary — 418 bytes total, independent of n (the
	// header itself is already exactly one Align block).
	for _, n := range []uint32{1, 3, 100, 1234, 999_999} {
		_, total := columnOffsets(canonicalColumns, n)
		unpadded := uint32(HeaderSize) + uint32(len(canonicalColumns))*dirEntrySize + n*uint32(1+2+4+4+8+8)
		if total < unpadded {
			t.Fatalf("n=%d: total %d < unpadded %d", n, total, unpadded)
		}
		if got := total - unpadded; got > 6*63+40 {
			t.Errorf("n=%d: padding overhead %d exceeds bound %d", n, got, 6*63+40)
		}
	}
}

func TestEncodedSizeMatchesFormula(t *testing.T) {
	// docs/docs/archive/06-toc-format.md's closed-form byte-size formula
	// (128-byte fixed header+directory, 27 bytes/element, plus bounded
	// per-column alignment padding), hand-computed independently of
	// columnOffsets -- .scratch/fblocks-ui/spec.md item 12 (live pool-
	// status-list TOC size needs this exact, not estimated).
	cases := []struct {
		n    uint32
		want uint32
	}{
		{0, 128},
		{1, 512},
		{100, 3072},
	}
	for _, c := range cases {
		if got := EncodedSize(c.n); got != c.want {
			t.Errorf("EncodedSize(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestEncodeDecodeEmptyColumns(t *testing.T) {
	c := &Columns{VersionMajor: 1, VersionMinor: 0, N: 0}
	buf, err := Encode(c)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// header (64) + column directory for 6 entries, padded to 64 (24 -> 64):
	// the 6 zero-length columns themselves contribute nothing, since 0 bytes
	// is already Align-aligned.
	const wantLen = 128
	if len(buf) != wantLen {
		t.Fatalf("encoded empty TOC len = %d, want %d (header + directory)", len(buf), wantLen)
	}
	got, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.N != 0 || got.VersionMajor != 1 {
		t.Errorf("decoded empty columns mismatch: %+v", got)
	}
}

func TestDecodeTooShort(t *testing.T) {
	_, err := Decode(make([]byte, 10))
	if err == nil {
		t.Fatal("expected error for buffer shorter than header")
	}
}

// TestDecodeSkipsUnknownTrailingColumn proves the forward-compatibility path
// a future minor version relies on (docs/docs/archive/06-toc-format.md
// §3.1): a directory entry of a kind this build doesn't recognize must still
// be skipped correctly using its own declared Width, without disturbing the
// six known columns' values. The buffer is hand-built (not via Encode, which
// only ever writes the canonical 6 entries) to simulate what a newer minor
// version would have written.
func TestDecodeSkipsUnknownTrailingColumn(t *testing.T) {
	n := uint32(2)
	entries := append(append([]dirEntry{}, canonicalColumns...), dirEntry{Kind: ColumnKind(999), Width: 4})

	offsets, total := columnOffsets(entries, n)
	buf := make([]byte, total)

	binary.LittleEndian.PutUint16(buf[0:2], 1)
	binary.LittleEndian.PutUint16(buf[2:4], 0)
	binary.LittleEndian.PutUint32(buf[4:8], n)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(len(entries)))
	for i, e := range entries {
		base := uint32(HeaderSize) + uint32(i)*dirEntrySize
		binary.LittleEndian.PutUint16(buf[base:base+2], uint16(e.Kind))
		buf[base+2] = e.Width
	}

	wantType := []mediatree.NodeType{mediatree.TypeVoid, mediatree.TypeUint32}
	wantRole := []mediatree.Role{mediatree.RoleRoot, mediatree.RoleChannel}
	wantParent := []uint32{0, 0}
	wantSibling := []uint32{0, 1}
	wantValueOrOffset := []uint64{0, 42}
	wantSize := []uint64{0, 0}

	offType, offRole, offParent := offsets[0], offsets[1], offsets[2]
	offSibling, offValueOrOffset, offSize := offsets[3], offsets[4], offsets[5]
	// offsets[6] (the unknown column) is left zeroed — Decode must never
	// need to interpret it.

	for i := uint32(0); i < n; i++ {
		buf[offType+i] = uint8(wantType[i])
	}
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint16(buf[offRole+i*2:offRole+i*2+2], uint16(wantRole[i]))
	}
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint32(buf[offParent+i*4:offParent+i*4+4], wantParent[i])
	}
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint32(buf[offSibling+i*4:offSibling+i*4+4], wantSibling[i])
	}
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint64(buf[offValueOrOffset+i*8:offValueOrOffset+i*8+8], wantValueOrOffset[i])
	}
	for i := uint32(0); i < n; i++ {
		binary.LittleEndian.PutUint64(buf[offSize+i*8:offSize+i*8+8], wantSize[i])
	}

	got, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.N != n {
		t.Fatalf("N = %d, want %d", got.N, n)
	}
	if !reflect.DeepEqual(got.Type, wantType) ||
		!reflect.DeepEqual(got.Role, wantRole) ||
		!reflect.DeepEqual(got.Parent, wantParent) ||
		!reflect.DeepEqual(got.Sibling, wantSibling) ||
		!reflect.DeepEqual(got.ValueOrOffset, wantValueOrOffset) ||
		!reflect.DeepEqual(got.Size, wantSize) {
		t.Fatalf("decoded columns mismatch with unknown trailing column present:\ngot %+v", got)
	}
}

func TestDecodeMissingRequiredColumn(t *testing.T) {
	// Directory with only 5 of the 6 required kinds (Size omitted).
	entries := append([]dirEntry{}, canonicalColumns[:5]...)
	_, total := columnOffsets(entries, 0)
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(len(entries)))
	for i, e := range entries {
		base := uint32(HeaderSize) + uint32(i)*dirEntrySize
		binary.LittleEndian.PutUint16(buf[base:base+2], uint16(e.Kind))
		buf[base+2] = e.Width
	}

	if _, err := Decode(buf); err == nil {
		t.Fatal("expected error for directory missing a required column kind")
	}
}

func TestDecodeDuplicateColumn(t *testing.T) {
	// Two Type entries, Size never declared.
	entries := append(append([]dirEntry{}, canonicalColumns[:5]...), dirEntry{ColumnKindType, 1})
	_, total := columnOffsets(entries, 0)
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[8:10], uint16(len(entries)))
	for i, e := range entries {
		base := uint32(HeaderSize) + uint32(i)*dirEntrySize
		binary.LittleEndian.PutUint16(buf[base:base+2], uint16(e.Kind))
		buf[base+2] = e.Width
	}

	if _, err := Decode(buf); err == nil {
		t.Fatal("expected error for directory with a duplicate column kind")
	}
}
