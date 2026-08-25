package storage

import (
	"fmt"
	"hash/crc32"

	"github.com/traycers/farc/fblock"
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// verifyWriteCompletionV2 reads fblock idx's v2.0 epilog (fixed offset,
// no scanning) and reports whether it's fully and cleanly written:
// Count==MaxEpilogRows and every one of the 5 rows' own node decodes
// without error. This is the v2.0 analog of v1.0's verifyWriteCompletion,
// but the returned Epilog also tells the caller exactly how many of the 5
// nodes are confirmed even when complete is false — a partial signal
// v1.0's binary WriteComplete/not-WriteComplete never had (ADR-023
// §"Решение"). An absent or malformed epilog (never written, or a crash
// before it existed at all) is reported as complete=false, not an error —
// same convention as v1.0's ErrIncompleteWrite/ErrUninitialized handling.
func verifyWriteCompletionV2(backend ioengine.Backend, geo Geometry, idx uint32) (fblockv2.Epilog, bool, error) {
	base := int64(fblockOffset(geo, idx))
	epilogOffset := base + int64(geo.FblockSize) - int64(fblockv2.EpilogSizeV2)

	epilogBuf := make([]byte, fblockv2.EpilogSizeV2)
	_, err := backend.ReadAt(epilogBuf, epilogOffset)
	if err != nil {
		return fblockv2.Epilog{}, false, nil //nolint:nilerr // a short/truncated read here IS an incomplete-write symptom (the epilog is the tail-most region), not a fault in the check itself -- same convention v1.0's verifyWriteCompletion used
	}
	epilog, err := fblockv2.DecodeEpilog(epilogBuf)
	if err != nil {
		return fblockv2.Epilog{}, false, nil //nolint:nilerr // an absent/corrupt epilog IS "not complete", not a fault in the check itself
	}
	if epilog.Count != fblockv2.MaxEpilogRows {
		return epilog, false, nil
	}

	for _, row := range epilog.Rows {
		nodeBuf := make([]byte, row.Size)
		_, err := backend.ReadAt(nodeBuf, base+int64(row.Offset))
		if err != nil {
			return epilog, false, err
		}
		_, _, err = fblockv2.DecodeNode(nodeBuf)
		if err != nil {
			return epilog, false, nil //nolint:nilerr // a node failing to decode IS "not complete"
		}
	}
	return epilog, true, nil
}

// recoverPartialWriteV2 is the v2.0 analog of v1.0's recoverPartialWrite:
// real, disk-writing recovery for a fblock whose epilog shows Count==3
// (root/params/catalog confirmed; content — the currently-open node — is
// not) or Count==4 (content also confirmed; only TOC is missing). Writes
// go directly via backend.WriteAt, not through StorageEngine — exactly
// like v1.0's version, since this runs at startup before any Engine/Unit
// exists. Count<3 (catalog not even trustworthy) or ==5 (nothing to
// recover) aren't this function's job.
func recoverPartialWriteV2(backend ioengine.Backend, geo Geometry, idx uint32, alignment int) (uuid [16]byte, begin, end uint64, ok bool, err error) {
	base := int64(fblockOffset(geo, idx))
	fblockEnd := base + int64(geo.FblockSize)
	epilogOffset := fblockEnd - int64(fblockv2.EpilogSizeV2)

	prologBuf := make([]byte, fblockv2.FixedPrologSizeV2)
	_, err = backend.ReadAt(prologBuf, base)
	if err != nil {
		return uuid, 0, 0, false, err
	}
	prolog, err := fblockv2.DecodeFixedProlog(prologBuf)
	if err != nil {
		return uuid, 0, 0, false, nil //nolint:nilerr // no valid prolog -> nothing to recover, not a fault in the check
	}

	epilogBuf := make([]byte, fblockv2.EpilogSizeV2)
	_, err = backend.ReadAt(epilogBuf, epilogOffset)
	if err != nil {
		return uuid, 0, 0, false, nil //nolint:nilerr // a short/truncated read here IS "nothing to recover", not a fault in the check itself
	}
	epilog, err := fblockv2.DecodeEpilog(epilogBuf)
	if err != nil || epilog.Count < 3 || epilog.Count > 4 {
		return uuid, 0, 0, false, nil //nolint:nilerr // not this function's case
	}

	catalogRow := epilog.Rows[2]
	catalogNodeBuf := make([]byte, catalogRow.Size)
	_, err = backend.ReadAt(catalogNodeBuf, base+int64(catalogRow.Offset))
	if err != nil {
		return uuid, 0, 0, false, err
	}
	catalogNode, _, err := fblockv2.DecodeNode(catalogNodeBuf)
	if err != nil {
		return uuid, 0, 0, false, nil //nolint:nilerr
	}
	catalog, err := fblock.DecodeCatalog(catalogNode.Value, prolog.MaxChannels, prolog.CatalogEntryCount)
	if err != nil {
		return uuid, 0, 0, false, nil //nolint:nilerr
	}
	uuid = catalog.UUID[idx]

	var elems []mediatree.Element
	var offsets []uint64

	if epilog.Count == 4 {
		// Content already finished normally -- decode it in full, no
		// trailer search needed.
		contentRow := epilog.Rows[3]
		contentNodeBuf := make([]byte, contentRow.Size)
		_, err = backend.ReadAt(contentNodeBuf, base+int64(contentRow.Offset))
		if err != nil {
			return uuid, 0, 0, false, err
		}
		contentNode, _, err := fblockv2.DecodeNode(contentNodeBuf)
		if err != nil {
			return uuid, 0, 0, false, nil //nolint:nilerr
		}
		elems, offsets, err = mediatree.DecodeContentWithOffsets(contentNode.Value)
		if err != nil {
			return uuid, 0, 0, false, nil //nolint:nilerr
		}
	} else {
		// Count==3: content is the currently-open node. Its internal
		// write-verify/MagicTrailer mechanism is unchanged from v1.0
		// (ADR-017), so the same FindTrailer/DecodeContentPartial
		// approach applies; only the outer bookkeeping (where content
		// starts, how to finalize its node header/tail) is v2.0-specific.
		contentOffset := catalogRow.Offset + catalogRow.Size
		valueRegionStart := base + int64(contentOffset) + int64(fblockv2.FixedHeaderSize)
		readLen := epilogOffset - valueRegionStart
		if readLen <= 0 {
			return uuid, 0, 0, false, nil
		}
		valueBuf := make([]byte, readLen)
		_, err = backend.ReadAt(valueBuf, valueRegionStart)
		if err != nil {
			return uuid, 0, 0, false, err
		}

		trailerOff, found := fblock.FindTrailer(valueBuf)
		if !found {
			return uuid, 0, 0, false, nil
		}
		recoveredValue := valueBuf[:trailerOff]
		elems, offsets = mediatree.DecodeContentPartial(recoveredValue)
		if len(elems) == 0 {
			return uuid, 0, 0, false, nil
		}

		// One combined write (header+value+padding+trailer, exactly
		// EncodeNode's own one-shot layout) rather than a separate
		// header-only write -- recovery already holds the whole value in
		// memory (valueBuf, above), and a real `direct` backend rejects a
		// standalone fixedHeaderSize-byte WriteAt outright (ADR-010: it's
		// never a multiple of Alignment()), the same problem
		// writeVerifyChunkLocked solves for the streaming write path
		// (internal/storageengine/engine.go) -- but recovery runs at
		// startup, before any Engine exists, so it must produce an
		// already-aligned write itself instead of relying on that fix.
		contentNode := fblockv2.Node{Type: fblockv2.NodeTypeContent, ID: 3, Parent: 0, Sibling: 2, Value: recoveredValue}
		contentEncoded, err := fblockv2.EncodeNode(contentNode, alignment)
		if err != nil {
			return uuid, 0, 0, false, err
		}
		_, err = backend.WriteAt(contentEncoded, base+int64(contentOffset))
		if err != nil {
			return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: write content node: %w", idx, err)
		}
		crc32Value := crc32.ChecksumIEEE(recoveredValue)

		contentTotalSize := uint64(fblockv2.NodeTotalSize(int64(len(recoveredValue)), alignment))
		epilog.Rows[3] = fblockv2.EpilogRow{Type: fblockv2.NodeTypeContent, ID: 3, Offset: contentOffset, Size: contentTotalSize, CRC32: crc32Value}
		epilog.Count = 4
		writeOffset, epilogBlock := writeEpilogAligned(fblockEnd, epilog, alignment)
		_, err = backend.WriteAt(epilogBlock, writeOffset)
		if err != nil {
			return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: write epilog (count=4): %w", idx, err)
		}
	}

	recoveredBegin, recoveredEnd, haveFrames := recoveredTimeRange(elems)
	if !haveFrames {
		return uuid, 0, 0, false, nil
	}

	columns, err := toc.Build(elems, offsets)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: build TOC: %w", idx, err)
	}
	tocBuf, err := toc.Encode(columns)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: encode TOC: %w", idx, err)
	}

	tocOffset := epilog.Rows[3].Offset + epilog.Rows[3].Size
	tocNode := fblockv2.Node{Type: fblockv2.NodeTypeTOC, ID: 4, Parent: 0, Sibling: 3, Value: tocBuf}
	tocEncoded, err := fblockv2.EncodeNode(tocNode, alignment)
	if err != nil {
		return uuid, 0, 0, false, err
	}
	_, err = backend.WriteAt(tocEncoded, base+int64(tocOffset))
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: write toc node: %w", idx, err)
	}
	epilog.Rows[4] = fblockv2.EpilogRow{Type: fblockv2.NodeTypeTOC, ID: 4, Offset: tocOffset, Size: uint64(len(tocEncoded)), CRC32: crc32.ChecksumIEEE(tocBuf)}
	epilog.Count = 5
	writeOffset, epilogBlock := writeEpilogAligned(fblockEnd, epilog, alignment)
	_, err = backend.WriteAt(epilogBlock, writeOffset)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("storage: recover v2 fblock %d: write epilog (count=5): %w", idx, err)
	}

	return uuid, recoveredBegin, recoveredEnd, true, nil
}
