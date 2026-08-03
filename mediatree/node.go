package mediatree

import (
	"encoding/binary"
	"fmt"
)

// ElementHeaderSize is the fixed-width portion of an encoded element:
// type(1) + role(2) + parent(4) + sibling(4) + size(8) = 19 bytes
// (docs/docs/archive/05-data-format.md §3).
const ElementHeaderSize = 19

// Element is one node of the Content tree. Id is not stored — it is the
// node's position in the sequence (docs/docs/archive/05-data-format.md §3).
type Element struct {
	Type    NodeType
	Role    Role
	Parent  uint32 // root: Parent == own id (self-reference)
	Sibling uint32 // first child: Sibling == own id (self-reference)
	Value   []byte
}

// EncodeElement serializes e as type+role+parent+sibling+size+value.
func EncodeElement(e Element) []byte {
	buf := make([]byte, ElementHeaderSize+len(e.Value))
	buf[0] = uint8(e.Type)
	binary.LittleEndian.PutUint16(buf[1:3], uint16(e.Role))
	binary.LittleEndian.PutUint32(buf[3:7], e.Parent)
	binary.LittleEndian.PutUint32(buf[7:11], e.Sibling)
	binary.LittleEndian.PutUint64(buf[11:19], uint64(len(e.Value)))
	copy(buf[19:], e.Value)
	return buf
}

// DecodeElement parses one element starting at the beginning of buf and
// returns it along with the number of bytes consumed, for sequential
// scanning (docs/docs/archive/05-data-format.md §3: Content is a sequence,
// not an id-indexable array, since element width varies).
func DecodeElement(buf []byte) (Element, int, error) {
	if len(buf) < ElementHeaderSize {
		return Element{}, 0, fmt.Errorf("mediatree: buffer too short for element header: %d < %d", len(buf), ElementHeaderSize)
	}
	t := NodeType(buf[0])
	if !t.Valid() {
		return Element{}, 0, fmt.Errorf("mediatree: unknown type code %d", buf[0])
	}
	size := binary.LittleEndian.Uint64(buf[11:19])
	total := ElementHeaderSize + int(size)
	if len(buf) < total {
		return Element{}, 0, fmt.Errorf("mediatree: buffer too short for element value: %d < %d", len(buf), total)
	}
	e := Element{
		Type:    t,
		Role:    Role(binary.LittleEndian.Uint16(buf[1:3])),
		Parent:  binary.LittleEndian.Uint32(buf[3:7]),
		Sibling: binary.LittleEndian.Uint32(buf[7:11]),
		Value:   append([]byte(nil), buf[19:total]...),
	}
	return e, total, nil
}

// EncodeContent serializes elems in order (their position in the slice IS
// their id) as a single Content byte sequence.
func EncodeContent(elems []Element) []byte {
	var total int
	for _, e := range elems {
		total += ElementHeaderSize + len(e.Value)
	}
	buf := make([]byte, 0, total)
	for _, e := range elems {
		buf = append(buf, EncodeElement(e)...)
	}
	return buf
}

// DecodeContent sequentially scans buf into a slice of elements — the
// TOC-independent fallback reconstruction path (docs/docs/archive/
// 05-data-format.md §3: "восстанавливать дерево последовательным
// сканированием Контента... если TOC утрачен или повреждён").
// A node with an unrecognized Role is still returned (never skipped
// silently) — callers that don't understand a given Role simply ignore
// those elements; only Type must be recognized to parse at all.
func DecodeContent(buf []byte) ([]Element, error) {
	var elems []Element
	off := 0
	for off < len(buf) {
		e, n, err := DecodeElement(buf[off:])
		if err != nil {
			return nil, fmt.Errorf("mediatree: decoding element at offset %d (id %d): %w", off, len(elems), err)
		}
		elems = append(elems, e)
		off += n
	}
	return elems, nil
}

// DecodeContentWithOffsets is DecodeContent plus, for each element, the byte
// offset (from the start of buf) where its Value begins — needed by TOC
// construction (docs/docs/archive/06-toc-format.md §4.1's value_or_offset
// union stores this offset for bytes/string values).
func DecodeContentWithOffsets(buf []byte) ([]Element, []uint64, error) {
	var elems []Element
	var offsets []uint64
	off := 0
	for off < len(buf) {
		e, n, err := DecodeElement(buf[off:])
		if err != nil {
			return nil, nil, fmt.Errorf("mediatree: decoding element at offset %d (id %d): %w", off, len(elems), err)
		}
		elems = append(elems, e)
		offsets = append(offsets, uint64(off+ElementHeaderSize))
		off += n
	}
	return elems, offsets, nil
}

// Children returns the ids of node parentID's direct children, in creation
// (= scan) order. No separate order field exists — scan order by id already
// is child order (docs/docs/archive/05-data-format.md §3).
func Children(elems []Element, parentID uint32) []uint32 {
	var kids []uint32
	for i, e := range elems {
		if uint32(i) != parentID && e.Parent == parentID {
			kids = append(kids, uint32(i))
		}
	}
	return kids
}

// FindChildByRole returns the first direct child of parentID with the given
// role, if any.
func FindChildByRole(elems []Element, parentID uint32, role Role) (uint32, bool) {
	for i, e := range elems {
		if uint32(i) != parentID && e.Parent == parentID && e.Role == role {
			return uint32(i), true
		}
	}
	return 0, false
}

// FindKeyframe walks the sibling chain of a video frame node backward
// (starting at and including frameID) until it finds one whose frame_kind
// child is FrameKindI — the only documented way to locate a GOP's keyframe;
// there is no dedicated parent-of-GOP link (docs/docs/archive/
// 07-media-tree.md §6). This is an O(chain length) Content-level scan,
// suitable for small scans or as a TOC-loss fallback; a TOC-backed version
// belongs one layer up (farc/toc-based query helpers).
func FindKeyframe(elems []Element, frameID uint32) (uint32, error) {
	id := frameID
	for {
		if kind, ok := findFrameKind(elems, id); ok && kind == FrameKindI {
			return id, nil
		}
		if elems[id].Sibling == id {
			return 0, fmt.Errorf("mediatree: no keyframe found walking back from frame %d", frameID)
		}
		id = elems[id].Sibling
	}
}

func findFrameKind(elems []Element, frameID uint32) (uint8, bool) {
	id, ok := FindChildByRole(elems, frameID, RoleFrameKind)
	if !ok || len(elems[id].Value) != 1 {
		return 0, false
	}
	return elems[id].Value[0], true
}
