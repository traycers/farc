package storage

import (
	"fmt"

	"github.com/traycers/farc/fblock"
)

// assembleHeaderAndMagic encodes h (whose Prolog.ParamsSize/CatalogSize/
// CatalogEntryCount/MaxChannels are overwritten in place by
// fblock.EncodeHeader, as a side effect callers downstream — e.g.
// assembleFblock's own contentCap calculation below — rely on) followed by
// magic_content, then zero-pads up to alignment (the backing backend's
// Alignment()) so the buffer's length exactly matches
// fblock.ComputeOffsets(..., alignment).ContentOffset — the same physical
// gap storageengine.EnqueueOpenWrite inserts (as headerPadLen) for a
// periodic-flush job. Reserving it here too, even though this one-shot path
// has no incremental-write alignment need of its own, means a reader never
// has to know which write path produced a given fblock (.scratch/
// fblocks-ui/issues/10-header-pad-content-offset-mismatch.md). This is
// exactly the prefix a promoted Segment hands storageengine.EnqueueOpenWrite
// as its open write job's first bytes (ADR-017), factored out so both that
// incremental path and the whole-buffer assembleFblock below share one
// implementation.
func assembleHeaderAndMagic(h *fblock.Header, alignment int) ([]byte, error) {
	headerBuf, err := fblock.EncodeHeader(h)
	if err != nil {
		return nil, fmt.Errorf("storage: assemble fblock: %w", err)
	}
	buf := make([]byte, 0, len(headerBuf)+len(fblock.MagicContent))
	buf = append(buf, headerBuf...)
	buf = append(buf, fblock.MagicContent[:]...)

	offs := fblock.ComputeOffsets(h.Prolog.ParamsSize, h.Prolog.CatalogSize, alignment)
	if gap := int64(offs.ContentOffset) - int64(len(buf)); gap > 0 {
		buf = append(buf, make([]byte, gap)...)
	}
	return buf, nil
}

// assembleFblock builds one complete, exactly-fblock_size-byte fblock
// buffer: header + magic_content + alignment gap + content zero-padded up
// to fblock.ContentSize + magic_toc + toc + epilog. content/toc may both be
// nil (the Storage-init write of fblock 0 — see this package's doc comment
// on why every fblock, including that one, still occupies its full nominal
// slot).
func assembleFblock(h *fblock.Header, content, tocBuf []byte, alignment int) ([]byte, error) {
	prefix, err := assembleHeaderAndMagic(h, alignment)
	if err != nil {
		return nil, err
	}
	tail, err := assembleTail(content, tocBuf, h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, alignment, 0)
	if err != nil {
		return nil, fmt.Errorf("storage: assemble fblock: %w", err)
	}

	buf := make([]byte, 0, len(prefix)+len(tail))
	buf = append(buf, prefix...)
	buf = append(buf, tail...)
	return buf, nil
}

// assembleTail computes the trailer shared by every path that ever finishes
// an fblock's content: full content zero-padded to fblock.ContentSize +
// magic_toc + toc + epilog, with CRC32Content always computed over the
// *entire* padded content regardless of alreadyWritten -- the exact
// on-disk-format invariant that used to be re-derived independently at
// each of this function's three callers (assembleFblock's own one-shot
// write, segment.go's writeTailLocked finishing an incremental open write,
// and consistency.go's recoverPartialWrite reconstructing a crashed
// in-progress fblock).
//
// alreadyWritten is how many of content's leading bytes are already
// physically on disk and must not be resent: 0 for a full rewrite
// (assembleFblock), the incrementally-flushed byte count for writeTailLocked,
// or len(content) for recoverPartialWrite (the recovered bytes are already
// on disk; only the trailer -- zero-pad onward -- needs (re)writing).
func assembleTail(content, tocBuf []byte, fblockSize uint64, paramsSize, catalogSize uint32, alignment int, alreadyWritten int64) ([]byte, error) {
	if alreadyWritten < 0 || alreadyWritten > int64(len(content)) {
		return nil, fmt.Errorf("storage: assemble tail: alreadyWritten %d out of range for %d bytes of content", alreadyWritten, len(content))
	}

	contentCap := fblock.ContentSize(fblockSize, paramsSize, catalogSize, uint32(len(tocBuf)), alignment)
	if contentCap < 0 {
		return nil, fmt.Errorf("storage: assemble tail: negative content capacity %d (fblock_size too small for this geometry/toc_size)", contentCap)
	}
	if int64(len(content)) > contentCap {
		return nil, fmt.Errorf("storage: assemble tail: content %d bytes exceeds capacity %d", len(content), contentCap)
	}

	padded := make([]byte, contentCap)
	copy(padded, content)
	epilog := fblock.Epilog{
		CRC32Content: fblock.CRC32(padded),
		CRC32TOC:     fblock.CRC32(tocBuf),
		TOCSize:      uint32(len(tocBuf)),
	}
	padLen := contentCap - int64(len(content))

	remainder := content[alreadyWritten:]
	tail := make([]byte, 0, int64(len(remainder))+padLen+int64(len(fblock.MagicTOC))+int64(len(tocBuf))+fblock.EpilogSize)
	tail = append(tail, remainder...)
	tail = append(tail, make([]byte, padLen)...)
	tail = append(tail, fblock.MagicTOC[:]...)
	tail = append(tail, tocBuf...)
	tail = append(tail, fblock.EncodeEpilog(epilog)...)
	return tail, nil
}
