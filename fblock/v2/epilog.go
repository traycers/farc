package v2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// MaxEpilogRows is the fixed number of top-level nodes for format v2.0
// (root/params/catalog/content/toc, §12.4) — the epilogue's row count never
// grows within this minor version, which is what keeps it at a fixed size
// and fixed offset from the end of the fblock (§12.5).
const MaxEpilogRows = 5

// magicEpilogStartV2 / magicEpilogFinishV2 are the epilogue's own magics —
// distinct from package fblock's v1.0 MagicEpilog.
var (
	magicEpilogStartV2  = [8]byte{'F', 'A', 'R', 'C', 'E', 'P', '2', 'S'}
	magicEpilogFinishV2 = [8]byte{'F', 'A', 'R', 'C', 'E', 'P', '2', 'F'}
)

// epilogRowSize is len(type)+len(id)+len(offset)+len(size)+len(crc32).
const epilogRowSize = 1 + 4 + 8 + 8 + 4

// EpilogSizeV2 is the fixed on-disk size of the v2.0 epilogue: magic_start +
// count + MaxEpilogRows rows + crc32_epilogue + magic_finish (§12.5).
const EpilogSizeV2 = 8 + 1 + MaxEpilogRows*epilogRowSize + 4 + 8

// EpilogRow describes one top-level node's real, physical location — found
// this way rather than by scanning (§12.5).
type EpilogRow struct {
	Type   NodeType
	ID     uint32
	Offset uint64
	Size   uint64
	// CRC32 mirrors the node's own crc32_value (§12.3) — not an independent
	// whole-node checksum: combining it with a header/tail CRC computed only
	// at Close (after a streamed value like content's has already gone by,
	// ADR-023 §"Решение") would need CRC32-combine polynomial arithmetic for
	// no real safety gain, since crc32_header/crc32_value inside the node
	// already catch any bit rot except in the (unchecked) padding.
	CRC32 uint32
}

// Epilog is the fblock's v2.0 terminal, non-tree directory structure
// (§12.5). Count grows monotonically 0..MaxEpilogRows as each node
// completes; rows at index >= Count are not yet meaningful.
type Epilog struct {
	Count uint8
	Rows  [MaxEpilogRows]EpilogRow
}

// EncodeEpilog serializes e into a new EpilogSizeV2-byte buffer.
func EncodeEpilog(e Epilog) []byte {
	buf := make([]byte, EpilogSizeV2)
	off := copy(buf, magicEpilogStartV2[:])
	buf[off] = e.Count
	off++

	for _, row := range e.Rows {
		buf[off] = byte(row.Type)
		off++
		binary.LittleEndian.PutUint32(buf[off:off+4], row.ID)
		off += 4
		binary.LittleEndian.PutUint64(buf[off:off+8], row.Offset)
		off += 8
		binary.LittleEndian.PutUint64(buf[off:off+8], row.Size)
		off += 8
		binary.LittleEndian.PutUint32(buf[off:off+4], row.CRC32)
		off += 4
	}

	bodyCRC := crc32.ChecksumIEEE(buf[0:off])
	binary.LittleEndian.PutUint32(buf[off:off+4], bodyCRC)
	off += 4
	copy(buf[off:], magicEpilogFinishV2[:])

	return buf
}

// DecodeEpilog parses the v2.0 epilogue from a EpilogSizeV2-byte buffer,
// verifying magic_start, crc32_epilogue (over magic_start..rows) and
// magic_finish.
func DecodeEpilog(buf []byte) (Epilog, error) {
	if len(buf) < EpilogSizeV2 {
		return Epilog{}, fmt.Errorf("fblock/v2: buffer too short for epilog: %d < %d", len(buf), EpilogSizeV2)
	}
	if [8]byte(buf[0:8]) != magicEpilogStartV2 {
		return Epilog{}, errors.New("fblock/v2: magic_start mismatch for epilog")
	}

	bodyEnd := 8 + 1 + MaxEpilogRows*epilogRowSize
	storedBodyCRC := binary.LittleEndian.Uint32(buf[bodyEnd : bodyEnd+4])
	if crc32.ChecksumIEEE(buf[0:bodyEnd]) != storedBodyCRC {
		return Epilog{}, errors.New("fblock/v2: crc32_epilogue mismatch")
	}
	if [8]byte(buf[bodyEnd+4:bodyEnd+12]) != magicEpilogFinishV2 {
		return Epilog{}, errors.New("fblock/v2: magic_finish mismatch for epilog")
	}

	var e Epilog
	off := 8
	e.Count = buf[off]
	off++
	for i := 0; i < MaxEpilogRows; i++ {
		e.Rows[i].Type = NodeType(buf[off])
		off++
		e.Rows[i].ID = binary.LittleEndian.Uint32(buf[off : off+4])
		off += 4
		e.Rows[i].Offset = binary.LittleEndian.Uint64(buf[off : off+8])
		off += 8
		e.Rows[i].Size = binary.LittleEndian.Uint64(buf[off : off+8])
		off += 8
		e.Rows[i].CRC32 = binary.LittleEndian.Uint32(buf[off : off+4])
		off += 4
	}

	return e, nil
}
