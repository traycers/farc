package storage

import (
	"fmt"

	"traycers/farc/fblock"
)

// assembleFblock builds one complete, exactly-fblock_size-byte fblock
// buffer: header (h, whose Prolog.ParamsSize/CatalogSize/CatalogEntryCount/
// MaxChannels are overwritten in place by fblock.EncodeHeader) + magic_content
// + content zero-padded up to fblock.ContentSize + magic_toc + toc + epilog.
// content/toc may both be nil (the Storage-init write of fblock 0 — see
// this package's doc comment on why every fblock, including that one,
// still occupies its full nominal slot).
func assembleFblock(h *fblock.Header, content, tocBuf []byte) ([]byte, error) {
	headerBuf, err := fblock.EncodeHeader(h)
	if err != nil {
		return nil, fmt.Errorf("storage: assemble fblock: %w", err)
	}

	contentCap := fblock.ContentSize(h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, uint32(len(tocBuf)))
	if contentCap < 0 {
		return nil, fmt.Errorf("storage: assemble fblock: negative content capacity %d (fblock_size too small for this geometry/toc_size)", contentCap)
	}
	if int64(len(content)) > contentCap {
		return nil, fmt.Errorf("storage: assemble fblock: fcontainer content %d bytes exceeds capacity %d for this write", len(content), contentCap)
	}
	padded := make([]byte, contentCap)
	copy(padded, content)

	epilog := fblock.Epilog{
		CRC32Content: fblock.CRC32(padded),
		CRC32TOC:     fblock.CRC32(tocBuf),
		TOCSize:      uint32(len(tocBuf)),
	}

	total := len(headerBuf) + 8 + len(padded) + 8 + len(tocBuf) + fblock.EpilogSize
	buf := make([]byte, 0, total)
	buf = append(buf, headerBuf...)
	buf = append(buf, fblock.MagicContent[:]...)
	buf = append(buf, padded...)
	buf = append(buf, fblock.MagicTOC[:]...)
	buf = append(buf, tocBuf...)
	buf = append(buf, fblock.EncodeEpilog(epilog)...)
	return buf, nil
}
