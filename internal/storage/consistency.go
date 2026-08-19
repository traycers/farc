package storage

import (
	"encoding/binary"
	"fmt"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/index"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// ConsistencyCheck resolves any in_progress fblock(s) left over from a
// crash (docs/docs/archive/04-storage-operations.md §5), after indices are
// already loaded into mgr via either Startup path.
//
// Unlike a plain header/epilog check, real recovery (recoverPartialWrite,
// below) DOES write to the main disk — reconstructing and physically
// writing a valid TOC+epilogue is the only way to make a partially-written
// fblock genuinely ready-readable again, a deliberate, documented exception
// to what this function used to guarantee. Every other outcome (Bad, or a
// clean CompleteWrite from an already-fully-written fblock) still only
// changes mgr's in-memory state; the caller is responsible for persisting
// that to the SSD catalog afterward (§5.1 step 3), since ConsistencyCheck
// itself doesn't know the catalog path.
//
// Recovering a *fully-written* (not partial) resolved fblock's UUID/begin/
// end doesn't require a separate disk read: mgr's own loaded snapshot
// already carries the correct values for that entry, thanks to the
// Recorder-side gap-fix described in this package's doc comment (both
// Startup paths load a catalog where the in-flight fblock's own entry was
// already patched with its real identity before being written). A
// genuinely partial fblock's UUID likewise comes from that same patched
// snapshot entry, but begin/end are instead recomputed from whatever
// frames actually decoded — the snapshot's End is only ever provisional
// (segment.go's promoteLocked patches it before a single frame has
// necessarily arrived) and can't be trusted as the true final value.
func ConsistencyCheck(backend ioengine.Backend, geo Geometry, mgr *index.Manager) error {
	cat := mgr.Snapshot()

	var inProgress []uint32
	for i := uint32(0); i < cat.N; i++ {
		if cat.State(i) == fblock.InProgress {
			inProgress = append(inProgress, i)
		}
	}
	if len(inProgress) == 0 {
		return nil
	}

	candidate := inProgress[0]
	if len(inProgress) > 1 {
		// §5.2: deep corruption. Keep only the max-write_sequence entry as
		// the candidate; every other in_progress fblock is Bad
		// unconditionally, no epilogue check needed.
		bestSeq := uint64(0)
		for _, idx := range inProgress {
			prolog, err := readFixedProlog(backend, geo, idx)
			if err == nil && prolog.WriteSequence >= bestSeq {
				bestSeq = prolog.WriteSequence
				candidate = idx
			}
		}
		for _, idx := range inProgress {
			if idx != candidate {
				err := mgr.MarkBad(idx)
				if err != nil {
					return fmt.Errorf("storage: consistency check: mark fblock %d bad: %w", idx, err)
				}
			}
		}
	}

	complete, err := verifyWriteCompletion(backend, geo, candidate)
	if err != nil {
		return fmt.Errorf("storage: consistency check: fblock %d: %w", candidate, err)
	}
	if complete {
		return mgr.CompleteWrite(candidate, cat.UUID[candidate], cat.Begin[candidate], cat.End[candidate])
	}

	uuid, begin, end, ok, err := recoverPartialWrite(backend, geo, candidate)
	if err != nil {
		return fmt.Errorf("storage: consistency check: recover fblock %d: %w", candidate, err)
	}
	if ok {
		return mgr.CompleteWrite(candidate, uuid, begin, end)
	}
	return mgr.MarkBad(candidate)
}

// recoverPartialWrite attempts ADR-017's real recovery for an in_progress
// fblock whose epilogue isn't valid (verifyWriteCompletion returned false)
// but whose header IS intact — meaning at least one periodic-flush trigger
// (segment.go's combined data+magic-trailer write) landed durably. It walks
// the raw content bytes decoding mediatree.Elements up to the last
// confirmed magic trailer (fblock.FindTrailer — unambiguous, since every
// earlier trigger's trailer was already overwritten by the next real
// content batch, only the most recent one ever survives on disk), builds a
// TOC from whatever decoded cleanly, and physically writes it plus a fresh
// epilogue, zero-padding from the trailer's old position onward exactly as
// a real Close would have. Returns ok=false (caller falls back to MarkBad)
// if the header itself isn't intact (a crash before even the first trigger
// completed), if no trailer can be found at all (not even one trigger
// landed), or if no frame timestamp can be recovered to determine end.
func recoverPartialWrite(backend ioengine.Backend, geo Geometry, idx uint32) (uuid [16]byte, begin, end uint64, ok bool, err error) {
	h, diag, err := readHeader(backend, geo, idx)
	if err != nil {
		return uuid, 0, 0, false, err
	}
	if diag.Status() != fblock.HeaderIntact {
		return uuid, 0, 0, false, nil
	}

	base := int64(fblockOffset(geo, idx))
	offs := fblock.ComputeOffsets(h.Prolog.ParamsSize, h.Prolog.CatalogSize, backend.Alignment())
	// tocSize=0 gives the largest possible content region -- a safe upper
	// bound to read (it never exceeds the fblock's own reserved slot,
	// since a real, nonzero TOC only ever shrinks this further).
	contentCap := fblock.ContentSize(h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, 0, backend.Alignment())
	if contentCap <= 0 {
		return uuid, 0, 0, false, nil
	}
	buf := make([]byte, contentCap)
	_, err = backend.ReadAt(buf, base+int64(offs.ContentOffset))
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("read content: %w", err)
	}

	trailerOff, found := fblock.FindTrailer(buf)
	if !found {
		return uuid, 0, 0, false, nil
	}
	elems, offsets := mediatree.DecodeContentPartial(buf[:trailerOff])
	if len(elems) == 0 {
		return uuid, 0, 0, false, nil
	}
	recoveredBegin, recoveredEnd, haveFrames := recoveredTimeRange(elems)
	if !haveFrames {
		return uuid, 0, 0, false, nil
	}

	columns, err := toc.Build(elems, offsets)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("build TOC: %w", err)
	}
	tocBuf, err := toc.Encode(columns)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("encode TOC: %w", err)
	}

	recoveredContent := buf[:trailerOff]
	tail, err := assembleTail(recoveredContent, tocBuf, h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, backend.Alignment(), trailerOff)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("assemble recovered tail: %w", err)
	}

	tailOffset := base + int64(offs.ContentOffset) + trailerOff
	_, err = backend.WriteAt(tail, tailOffset)
	if err != nil {
		return uuid, 0, 0, false, fmt.Errorf("write recovered tail: %w", err)
	}

	return h.Catalog.UUID[idx], recoveredBegin, recoveredEnd, true, nil
}

// recoveredTimeRange scans elems for every frame timestamp (video or
// audio) and returns their min/max — the recovered fblock's true begin/end,
// independent of the catalog snapshot's own provisional Begin/End (see
// this file's package-level doc comment above).
func recoveredTimeRange(elems []mediatree.Element) (begin, end uint64, ok bool) {
	for _, e := range elems {
		if e.Role != mediatree.RoleFrameTimeVideo && e.Role != mediatree.RoleFrameTimeAudio {
			continue
		}
		if len(e.Value) != 8 {
			continue
		}
		t := binary.LittleEndian.Uint64(e.Value)
		if !ok {
			begin, end, ok = t, t, true
			continue
		}
		if t < begin {
			begin = t
		}
		if t > end {
			end = t
		}
	}
	return begin, end, ok
}

// verifyWriteCompletion reads fblock idx's own header+epilog+content+TOC
// from the main disk and reports whether the write reached WriteComplete
// (docs/docs/archive/03-storage-format.md §9.1, fblock.EpilogDiagnosis) —
// the only outcome that maps to Ready; everything else maps to Bad.
func verifyWriteCompletion(backend ioengine.Backend, geo Geometry, idx uint32) (bool, error) {
	h, diag, err := readHeader(backend, geo, idx)
	if err != nil {
		return false, fmt.Errorf("read header: %w", err)
	}
	if diag.Status() != fblock.HeaderIntact {
		return false, nil // header itself untrustworthy -> Bad
	}

	epilog, err := readEpilog(backend, geo, idx)
	if err != nil {
		return false, nil //nolint:nilerr // ErrIncompleteWrite (or a short read) IS this function's "Bad" determination, not a fault in the check itself
	}

	contentSize := fblock.ContentSize(h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, epilog.TOCSize, backend.Alignment())
	if contentSize < 0 {
		return false, nil
	}
	base := int64(fblockOffset(geo, idx))
	offs := fblock.ComputeOffsets(h.Prolog.ParamsSize, h.Prolog.CatalogSize, backend.Alignment())

	contentBuf := make([]byte, contentSize)
	_, err = backend.ReadAt(contentBuf, base+int64(offs.ContentOffset))
	if err != nil {
		return false, fmt.Errorf("read content: %w", err)
	}
	tocBuf := make([]byte, epilog.TOCSize)
	if epilog.TOCSize > 0 {
		tocStart := base + int64(geo.FblockSize) - int64(fblock.TOCOffsetFromEnd(epilog.TOCSize))
		_, err = backend.ReadAt(tocBuf, tocStart)
		if err != nil {
			return false, fmt.Errorf("read toc: %w", err)
		}
	}

	epilogDiag := fblock.EpilogDiagnosis{
		EpilogValid:  true,
		ContentValid: fblock.CRC32(contentBuf) == epilog.CRC32Content,
		TOCValid:     fblock.CRC32(tocBuf) == epilog.CRC32TOC,
	}
	return epilogDiag.Status() == fblock.WriteComplete, nil
}
