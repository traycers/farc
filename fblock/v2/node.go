// Package v2 implements the fblock format v2.0 (ADR-023, not yet accepted
// into the read/write path): a tree of TLV nodes for the fblock's
// top-level sections, plus a progressively-updated epilogue directory.
// Unlike package fblock (v1.0, the format actually read/written by farcd
// today), this package is not wired into any production code path.
package v2

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// NodeType is the closed set of top-level node kinds for format v2.0
// (docs/docs/archive/03-storage-format.md §12.3).
type NodeType uint8

const (
	NodeTypeRoot NodeType = iota
	NodeTypeParams
	NodeTypeCatalog
	NodeTypeContent
	NodeTypeTOC
)

// nodeMagicStart maps each NodeType to its 8-byte magic_start, per §12.3
// ("Уникальная для каждого type метка начала").
var nodeMagicStart = map[NodeType][8]byte{
	NodeTypeRoot:    {'F', 'A', 'R', 'C', 'N', 'R', 'O', 'O'},
	NodeTypeParams:  {'F', 'A', 'R', 'C', 'N', 'P', 'A', 'R'},
	NodeTypeCatalog: {'F', 'A', 'R', 'C', 'N', 'C', 'A', 'T'},
	NodeTypeContent: {'F', 'A', 'R', 'C', 'N', 'C', 'N', 'T'},
	NodeTypeTOC:     {'F', 'A', 'R', 'C', 'N', 'T', 'O', 'C'},
}

// magicNodeFinish marks the end of a node, shared across all NodeTypes —
// unlike magic_start, it needs no per-type distinction: by the time it's
// checked, magic_start (type-specific) and both CRCs already have been.
var magicNodeFinish = [8]byte{'F', 'A', 'R', 'C', 'N', 'F', 'I', 'N'}

// fixedHeaderSize is len(magic_start)+len(type)+len(id)+len(parent)+
// len(sibling)+len(value_size) — the bytes before value (§12.3).
const fixedHeaderSize = 8 + 1 + 4 + 4 + 4 + 8

// FixedHeaderSize exports fixedHeaderSize for callers that open a node's
// value region for streaming writes before the header itself is known
// (only value_size is missing, not the other header fields — but it's
// still not known until the streamed value finishes, ADR-023
// §"Решение") — the value always starts exactly FixedHeaderSize bytes
// into the node, regardless of when the header gets physically written.
const FixedHeaderSize = fixedHeaderSize

// trailerSize is len(crc32_header)+len(crc32_value)+len(magic_finish).
const trailerSize = 4 + 4 + 8

// Node is a single verkhneurovnevy (top-level) fblock node for format v2.0.
type Node struct {
	Type    NodeType
	ID      uint32
	Parent  uint32
	Sibling uint32
	Value   []byte
}

// EncodeNode serializes n as magic_start|type|id|parent|sibling|value_size|
// value|padding|crc32_header|crc32_value|magic_finish, padded so the whole
// node is a multiple of alignment bytes (§12.3).
func EncodeNode(n Node, alignment int) ([]byte, error) {
	header, crc32Header, err := EncodeNodeHeader(n.Type, n.ID, n.Parent, n.Sibling, int64(len(n.Value)))
	if err != nil {
		return nil, err
	}
	tail := EncodeNodeTail(int64(len(n.Value)), alignment, crc32Header, crc32.ChecksumIEEE(n.Value))

	buf := make([]byte, 0, len(header)+len(n.Value)+len(tail))
	buf = append(buf, header...)
	buf = append(buf, n.Value...)
	buf = append(buf, tail...)
	return buf, nil
}

// EncodeNodeHeader returns just a node's fixed fixedHeaderSize-byte header
// (magic_start|type|id|parent|sibling|value_size) and its crc32_header —
// usable standalone when a node's value is streamed rather than known
// upfront (a content node's real value_size isn't known until its
// open-write job closes, ADR-023 §"Решение") — EncodeNode above is just
// this plus EncodeNodeTail with the value glued in between.
func EncodeNodeHeader(nodeType NodeType, id, parent, sibling uint32, valueSize int64) (header []byte, crc32Header uint32, err error) {
	magicStart, ok := nodeMagicStart[nodeType]
	if !ok {
		return nil, 0, fmt.Errorf("fblock/v2: unknown node type %d", nodeType)
	}

	buf := make([]byte, fixedHeaderSize)
	off := copy(buf, magicStart[:])
	buf[off] = byte(nodeType)
	off++
	binary.LittleEndian.PutUint32(buf[off:off+4], id)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:off+4], parent)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:off+4], sibling)
	off += 4
	binary.LittleEndian.PutUint64(buf[off:off+8], uint64(valueSize))

	return buf, crc32.ChecksumIEEE(buf[8:]), nil
}

// EncodeNodeTail returns padding-to-alignment + crc32_header + crc32_value
// + magic_finish for a node whose value is valueSize bytes. crc32Value must
// already cover exactly those valueSize bytes — computed however the
// caller likes, including incrementally as the value streamed in, so a
// large content node never needs to be read back to produce this (ADR-023
// §"Решение").
func EncodeNodeTail(valueSize int64, alignment int, crc32Header, crc32Value uint32) []byte {
	unpadded := fixedHeaderSize + int(valueSize) + trailerSize
	total := roundUp(unpadded, alignment)
	padLen := total - unpadded

	tail := make([]byte, padLen+trailerSize)
	off := padLen // padding left zero
	binary.LittleEndian.PutUint32(tail[off:off+4], crc32Header)
	off += 4
	binary.LittleEndian.PutUint32(tail[off:off+4], crc32Value)
	off += 4
	copy(tail[off:], magicNodeFinish[:])
	return tail
}

// NodeTotalSize returns a node's total on-disk footprint (header+value+
// padding+trailer) for a value of valueSize bytes, without encoding or
// decoding anything — e.g. for an epilog row's Size before the node's
// bytes are all in one buffer to measure.
func NodeTotalSize(valueSize int64, alignment int) int64 {
	return int64(roundUp(fixedHeaderSize+int(valueSize)+trailerSize, alignment))
}

// DecodeNode parses a single node from the start of buf, verifying
// crc32_header, crc32_value and both magics. buf must be exactly the
// node's own bytes (e.g. buf[row.Offset:row.Offset+row.Size] from an
// epilog row) — padding is derived from len(buf) minus the known
// header/value/trailer sizes, not recomputed from an assumed alignment,
// so decoding never depends on which alignment the node happened to be
// written with (a real bug: a node written with one backend's Alignment()
// and later read back assuming a different one would otherwise look
// truncated). consumed is simply len(buf) once decoding succeeds.
func DecodeNode(buf []byte) (n Node, consumed int, err error) {
	if len(buf) < fixedHeaderSize {
		return Node{}, 0, fmt.Errorf("fblock/v2: buffer too short for node header: %d < %d", len(buf), fixedHeaderSize)
	}

	nodeType := NodeType(buf[8])
	wantMagic, ok := nodeMagicStart[nodeType]
	if !ok {
		return Node{}, 0, fmt.Errorf("fblock/v2: unknown node type %d", nodeType)
	}
	if [8]byte(buf[0:8]) != wantMagic {
		return Node{}, 0, fmt.Errorf("fblock/v2: magic_start mismatch for type %d", nodeType)
	}

	id := binary.LittleEndian.Uint32(buf[9:13])
	parent := binary.LittleEndian.Uint32(buf[13:17])
	sibling := binary.LittleEndian.Uint32(buf[17:21])
	valueSize := binary.LittleEndian.Uint64(buf[21:29])

	unpadded := fixedHeaderSize + int(valueSize) + trailerSize
	if len(buf) < unpadded {
		return Node{}, 0, fmt.Errorf("fblock/v2: buffer too short for node body: %d < %d", len(buf), unpadded)
	}
	total := len(buf)

	headerCRC := crc32.ChecksumIEEE(buf[8:fixedHeaderSize])
	value := buf[fixedHeaderSize : fixedHeaderSize+int(valueSize)]

	trailerOff := total - trailerSize
	storedHeaderCRC := binary.LittleEndian.Uint32(buf[trailerOff : trailerOff+4])
	storedValueCRC := binary.LittleEndian.Uint32(buf[trailerOff+4 : trailerOff+8])
	finishMagic := [8]byte(buf[trailerOff+8 : trailerOff+16])

	if headerCRC != storedHeaderCRC {
		return Node{}, 0, fmt.Errorf("fblock/v2: crc32_header mismatch for node id=%d", id)
	}
	if crc32.ChecksumIEEE(value) != storedValueCRC {
		return Node{}, 0, fmt.Errorf("fblock/v2: crc32_value mismatch for node id=%d", id)
	}
	if finishMagic != magicNodeFinish {
		return Node{}, 0, fmt.Errorf("fblock/v2: magic_finish mismatch for node id=%d", id)
	}

	return Node{
		Type:    nodeType,
		ID:      id,
		Parent:  parent,
		Sibling: sibling,
		Value:   value,
	}, total, nil
}

// roundUp rounds n up to the nearest multiple of alignment. alignment <= 1
// means "no alignment requirement" and is a no-op — same convention as
// fblock.ComputeOffsets (fblock/geometry.go).
func roundUp(n, alignment int) int {
	if alignment <= 1 {
		return n
	}
	if rem := n % alignment; rem != 0 {
		return n + (alignment - rem)
	}
	return n
}
