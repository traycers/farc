package storage

import (
	"fmt"

	"traycers/farc/fblock"
	"traycers/farc/internal/index"
	"traycers/farc/internal/ioengine"
)

// ConsistencyCheck resolves any in_progress fblock(s) left over from a
// crash (docs/docs/archive/04-storage-operations.md §5), after indices are
// already loaded into mgr via either Startup path. It never writes to the
// main disk — only mgr's in-memory state changes; the caller is
// responsible for persisting that to the SSD catalog afterward (§5.1 step
// 3), since ConsistencyCheck itself doesn't know the catalog path.
//
// Recovering the resolved fblock's UUID/begin/end doesn't require a
// separate disk read: mgr's own loaded snapshot already carries the
// correct values for that entry, thanks to the Recorder-side gap-fix
// described in this package's doc comment (both Startup paths load a
// catalog where the in-flight fblock's own entry was already patched with
// its real identity before being written).
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
	return mgr.MarkBad(candidate)
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

	contentSize := fblock.ContentSize(h.Prolog.FblockSize, h.Prolog.ParamsSize, h.Prolog.CatalogSize, epilog.TOCSize)
	if contentSize < 0 {
		return false, nil
	}
	base := int64(fblockOffset(geo, idx))
	offs := fblock.ComputeOffsets(h.Prolog.ParamsSize, h.Prolog.CatalogSize)

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
