package storage

import "github.com/traycers/farc/fblock"

// beginFblockWrite is the fblock write transaction's begin phase, shared by
// segmentImpl's promoteLocked and closeLocked's retry loop: selects the
// next physical index, transitions it to in_progress, publishes
// fblock.write.started (and fblock.deleted if the slot was previously
// Ready), sets the segment's channels' channel_bitmap bits, patches a
// catalog snapshot with the new fcontainer's identity, bumps
// write_sequence, and best-effort mirrors the result to the SSD catalog.
//
// Returns the selected index and a fully populated *fblock.Header --
// callers differ only in what they do next (assembleHeaderAndMagic +
// EnqueueOpenWrite for an as-yet-contentless open write, vs assembleFblock
// + EnqueueWrite once content/TOC are already known), so that choice is
// left to them.
func (u *Unit) beginFblockWrite(now uint64, uuid [16]byte, positions map[uint16]uint16, begin, end uint64) (uint32, *fblock.Header, error) {
	idx, err := u.mgr.SelectNextIndex(now)
	if err != nil {
		u.notify.Publish(Event{Name: EventStorageAlert, Severity: "critical", Reason: AlertNoFreeFblocks})
		return 0, nil, err
	}

	prev := u.mgr.Snapshot()
	wasReady := prev.State(idx) == fblock.Ready
	prevUUID := prev.UUID[idx]

	err = u.mgr.BeginWrite(idx)
	if err != nil {
		return 0, nil, err
	}
	u.notify.Publish(Event{Name: EventFblockWriteStarted, Index: idx, UUID: uuid})
	if wasReady {
		u.notify.Publish(Event{Name: EventFblockDeleted, Index: idx, UUID: prevUUID})
	}

	for _, pos := range positions {
		err = u.mgr.SetChannelBit(idx, pos, true)
		if err != nil {
			return 0, nil, err
		}
	}

	snap := u.mgr.Snapshot()
	snap.UUID[idx] = uuid
	snap.Begin[idx] = begin
	snap.End[idx] = end

	seq := u.nextWriteSequence()
	u.saveSSDCatalogBestEffort(snap, SSDCatalogMeta{WriteSequence: seq, CatalogTime: now, Cursor: idx})

	h := &fblock.Header{
		Prolog: fblock.FixedProlog{
			FormatVersionMajor: 1,
			FormatVersionMinor: 0,
			MaxChannels:        u.geo.MaxChannels,
			WriteSequence:      seq,
			CatalogTime:        now,
			FblockSize:         u.geo.FblockSize,
		},
		Params:  u.currentParams(),
		Catalog: snap,
	}
	return idx, h, nil
}

// completeFblockWrite is the transaction's complete phase: transitions idx
// to Ready with the written fcontainer's identity, mirrors the result to
// the SSD catalog, publishes fblock.write.completed, and records the
// write's health/bad-ratio impact. Excludes pool.release -- that needs the
// caller's own *segmentImpl (as a poolSlot), which this method has no
// business knowing about.
func (u *Unit) completeFblockWrite(idx uint32, uuid [16]byte, begin, end, seq, now uint64) error {
	u.health.RecordWrite(false)
	err := u.mgr.CompleteWrite(idx, uuid, begin, end)
	if err != nil {
		return err
	}
	u.saveSSDCatalogBestEffort(u.mgr.Snapshot(), SSDCatalogMeta{WriteSequence: seq, CatalogTime: now, Cursor: idx})
	u.notify.Publish(Event{Name: EventFblockWriteCompleted, Index: idx, UUID: uuid})
	u.health.CheckBadRatio(u.mgr)
	return nil
}

// failFblockWrite is the transaction's fail phase: records the write's
// health impact, publishes fblock.write.failed, and marks idx Bad.
// Excludes pool.release/segmentImpl.closed -- left to each of the four call
// sites, which differ in what they do afterward (return ErrSegmentClosed,
// or continue a retry loop).
func (u *Unit) failFblockWrite(idx uint32, uuid [16]byte) error {
	u.health.RecordWrite(true)
	u.notify.Publish(Event{Name: EventFblockWriteFailed, Index: idx, UUID: uuid})
	return u.mgr.MarkBad(idx)
}
