package v2

import (
	"bytes"
	"hash/crc32"
	"testing"
)

func TestNodeRoundTrip(t *testing.T) {
	n := Node{
		Type:    NodeTypeParams,
		ID:      1,
		Parent:  0,
		Sibling: 0,
		Value:   []byte(`{"fchunk_size":4194304}`),
	}

	buf, err := EncodeNode(n, 64)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if len(buf)%64 != 0 {
		t.Fatalf("encoded node size %d not aligned to 64", len(buf))
	}

	got, consumed, err := DecodeNode(buf)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	if consumed != len(buf) {
		t.Fatalf("consumed = %d, want %d", consumed, len(buf))
	}
	if got.Type != n.Type || got.ID != n.ID || got.Parent != n.Parent || got.Sibling != n.Sibling {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, n)
	}
	if !bytes.Equal(got.Value, n.Value) {
		t.Fatalf("value mismatch: got %q, want %q", got.Value, n.Value)
	}
}

func TestNodeRoundTripNoAlignment(t *testing.T) {
	n := Node{Type: NodeTypeTOC, ID: 4, Parent: 0, Sibling: 3, Value: []byte("toc-bytes")}

	buf, err := EncodeNode(n, 1)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}

	got, consumed, err := DecodeNode(buf)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	if consumed != len(buf) {
		t.Fatalf("consumed = %d, want %d", consumed, len(buf))
	}
	if !bytes.Equal(got.Value, n.Value) {
		t.Fatalf("value mismatch: got %q, want %q", got.Value, n.Value)
	}
}

func TestNodeRoundTripEmptyValue(t *testing.T) {
	// The root node (id=0) has no value (§12.4).
	n := Node{Type: NodeTypeRoot, ID: 0, Parent: 0, Sibling: 0}

	buf, err := EncodeNode(n, 64)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}

	got, _, err := DecodeNode(buf)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	if len(got.Value) != 0 {
		t.Fatalf("value = %q, want empty", got.Value)
	}
}

func TestDecodeNodeCorruptValueCRC(t *testing.T) {
	n := Node{Type: NodeTypeParams, ID: 1, Value: []byte("hello")}
	buf, err := EncodeNode(n, 64)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	buf[fixedHeaderSize] ^= 0xFF // flip a bit inside value

	if _, _, err := DecodeNode(buf); err == nil {
		t.Fatalf("expected error for corrupted value, got nil")
	}
}

func TestDecodeNodeCorruptFinishMagic(t *testing.T) {
	n := Node{Type: NodeTypeParams, ID: 1, Value: []byte("hello")}
	buf, err := EncodeNode(n, 64)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	buf[len(buf)-1] ^= 0xFF // flip a bit inside magic_finish

	if _, _, err := DecodeNode(buf); err == nil {
		t.Fatalf("expected error for corrupted magic_finish, got nil")
	}
}

func TestEncodeNodeUnknownType(t *testing.T) {
	_, err := EncodeNode(Node{Type: NodeType(99)}, 64)
	if err == nil {
		t.Fatalf("expected error for unknown node type, got nil")
	}
}

func TestDecodeNodeTooShort(t *testing.T) {
	if _, _, err := DecodeNode(make([]byte, 10)); err == nil {
		t.Fatalf("expected error for too-short buffer, got nil")
	}
}

// TestStreamedNodeMatchesEncodeNode proves the streaming path (separately
// encoded header, appended value chunks, then a tail built from an
// incrementally-tracked CRC32 rather than the whole value) produces
// byte-for-byte the same node as one-shot EncodeNode — the mechanism
// content's open-write path needs, since its real value_size isn't known
// until Close (ADR-023 §"Решение").
func TestStreamedNodeMatchesEncodeNode(t *testing.T) {
	value := []byte("streamed-content-value-bytes")
	n := Node{Type: NodeTypeContent, ID: 3, Parent: 0, Sibling: 2, Value: value}

	want, err := EncodeNode(n, 64)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}

	header, crc32Header, err := EncodeNodeHeader(n.Type, n.ID, n.Parent, n.Sibling, int64(len(value)))
	if err != nil {
		t.Fatalf("EncodeNodeHeader: %v", err)
	}

	hasher := crc32.NewIEEE()
	// Simulate Append being called in two chunks, as content would arrive
	// incrementally rather than all at once.
	chunk1, chunk2 := value[:10], value[10:]
	hasher.Write(chunk1)
	hasher.Write(chunk2)
	crc32Value := hasher.Sum32()

	tail := EncodeNodeTail(int64(len(value)), 64, crc32Header, crc32Value)

	got := append(append(append([]byte{}, header...), value...), tail...)
	if int64(len(got)) != NodeTotalSize(int64(len(value)), 64) {
		t.Fatalf("streamed total size = %d, want %d", len(got), NodeTotalSize(int64(len(value)), 64))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("streamed encoding != EncodeNode:\ngot:  %x\nwant: %x", got, want)
	}

	decoded, consumed, err := DecodeNode(got)
	if err != nil {
		t.Fatalf("DecodeNode(streamed): %v", err)
	}
	if consumed != len(got) || !bytes.Equal(decoded.Value, value) {
		t.Fatalf("decode mismatch: consumed=%d len=%d value=%q", consumed, len(got), decoded.Value)
	}
}

func TestEncodeNodeHeaderUnknownType(t *testing.T) {
	if _, _, err := EncodeNodeHeader(NodeType(99), 0, 0, 0, 0); err == nil {
		t.Fatalf("expected error for unknown node type, got nil")
	}
}
