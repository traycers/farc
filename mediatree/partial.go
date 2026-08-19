package mediatree

// DecodeContentPartial scans buf sequentially like DecodeContentWithOffsets,
// but never errors: it returns everything that decoded cleanly before the
// first failure (insufficient bytes for a header/value, or an unrecognized
// type). Used by crash recovery (docs/docs/archive/
// adr/017-periodic-fchunk-flush.md) to reconstruct a TOC from a
// partially-written in_progress fblock's content, up to its last confirmed
// magic trailer — the trailer's position bounds how much of buf is ever
// passed in, so a truncated final element here is expected, not an error.
func DecodeContentPartial(buf []byte) (elems []Element, offsets []uint64) {
	off := 0
	for off < len(buf) {
		e, n, err := DecodeElement(buf[off:])
		if err != nil {
			break
		}
		elems = append(elems, e)
		offsets = append(offsets, uint64(off+ElementHeaderSize))
		off += n
	}
	return elems, offsets
}
