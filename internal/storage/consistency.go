package storage

import (
	"encoding/binary"
	"fmt"

	"github.com/traycers/farc/fblock"
	fblockv2 "github.com/traycers/farc/fblock/v2"
	"github.com/traycers/farc/internal/index"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/mediatree"
)

// ConsistencyCheck resolves any in_progress fblock(s) left over from a
// crash (docs/docs/archive/04-storage-operations.md §5), after indices are
// already loaded into mgr via either Startup path.
//
// Unlike a plain header/epilog check, real recovery (recoverPartialWriteV2,
// consistency_v2.go) DOES write to the main disk — reconstructing and physically
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
			buf := make([]byte, fblockv2.FixedPrologSizeV2)
			_, err := backend.ReadAt(buf, int64(fblockOffset(geo, idx)))
			if err != nil {
				continue
			}
			prolog, err := fblockv2.DecodeFixedProlog(buf)
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

	alignment := backend.Alignment()
	_, complete, err := verifyWriteCompletionV2(backend, geo, candidate)
	if err != nil {
		return fmt.Errorf("storage: consistency check: fblock %d: %w", candidate, err)
	}
	if complete {
		return mgr.CompleteWrite(candidate, cat.UUID[candidate], cat.Begin[candidate], cat.End[candidate])
	}

	uuid, begin, end, ok, err := recoverPartialWriteV2(backend, geo, candidate, alignment)
	if err != nil {
		return fmt.Errorf("storage: consistency check: recover fblock %d: %w", candidate, err)
	}
	if ok {
		return mgr.CompleteWrite(candidate, uuid, begin, end)
	}
	return mgr.MarkBad(candidate)
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
