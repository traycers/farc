package storage

import (
	"fmt"

	"traycers/farc/fblock"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// WriteFcontainer performs docs/docs/archive/04-storage-operations.md §7
// end to end for one already-filled fcontainer: resolve channels to
// compact positions, select a physical index, assemble the fblock and
// write it with verify, retrying on a corrupted write with a fresh index
// (§7.3 step 1), and commit + publish on success. now is Unix ns, used for
// select_next_index's retention check and as this write's catalog_time.
//
// See this package's doc comment for the two Recorder-side gap-fixes this
// method implements: patching the fblock's own catalog entry with the new
// fcontainer's real UUID/begin/end/channel_bitmap before writing (so a
// crash after a successful write can still be recovered), and saving the
// SSD catalog both at BeginWrite and at CompleteWrite (so the SSD-catalog
// Startup path can detect an in-flight write's success too).
//
// ADR-017's incremental flush is not implemented — filler must already be
// fully populated (its fcontainer closed) before this call.
func (u *Unit) WriteFcontainer(channels []uint16, begin, end uint64, filler *fcontainer.Filler, now uint64) ([16]byte, error) {
	u.writeMu.Lock()
	defer u.writeMu.Unlock()

	elems := filler.Elements()
	contentBuf := mediatree.EncodeContent(elems)
	_, valueOffsets, err := mediatree.DecodeContentWithOffsets(contentBuf)
	if err != nil {
		return [16]byte{}, fmt.Errorf("storage: recorder: re-decode content offsets: %w", err)
	}
	columns, err := toc.Build(elems, valueOffsets)
	if err != nil {
		return [16]byte{}, fmt.Errorf("storage: recorder: build TOC: %w", err)
	}
	tocBuf, err := toc.Encode(columns)
	if err != nil {
		return [16]byte{}, fmt.Errorf("storage: recorder: encode TOC: %w", err)
	}

	uuid, err := newUUIDv4()
	if err != nil {
		return [16]byte{}, err
	}

	positions, err := u.mgr.RegisterChannels(channels)
	if err != nil {
		return [16]byte{}, fmt.Errorf("storage: recorder: register channels: %w", err)
	}

	params := u.currentParams()

	for {
		idx, err := u.mgr.SelectNextIndex(now)
		if err != nil {
			u.notify.Publish(Event{Name: EventStorageAlert, Severity: "critical", Reason: AlertNoFreeFblocks})
			return [16]byte{}, err
		}

		prev := u.mgr.Snapshot()
		wasReady := prev.State(idx) == fblock.Ready
		prevUUID := prev.UUID[idx]

		err = u.mgr.BeginWrite(idx)
		if err != nil {
			return [16]byte{}, err
		}
		u.notify.Publish(Event{Name: EventFblockWriteStarted, Index: idx, UUID: uuid})
		if wasReady {
			u.notify.Publish(Event{Name: EventFblockDeleted, Index: idx, UUID: prevUUID})
		}

		// Gap-fix: patch this write's own catalog entry with its real
		// identity before it's embedded in the header (see package doc).
		// The channel bits also need setting on IndexManager's own live
		// catalog — CompleteWrite below only ever touches state/uuid/
		// begin/end, never channel_bitmap (see index.Manager.SetChannelBit).
		for _, pos := range positions {
			err := u.mgr.SetChannelBit(idx, pos, true)
			if err != nil {
				return [16]byte{}, err
			}
		}
		snap := u.mgr.Snapshot()
		snap.UUID[idx] = uuid
		snap.Begin[idx] = begin
		snap.End[idx] = end

		seq := u.nextWriteSequence()

		// Gap-fix: mirror in-flight visibility to the SSD catalog too, so
		// path 1 can detect this write's success even if the process
		// crashes before CompleteWrite runs.
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
			Params:  params,
			Catalog: snap,
		}
		buf, err := assembleFblock(h, contentBuf, tocBuf)
		if err != nil {
			return [16]byte{}, fmt.Errorf("storage: recorder: assemble fblock %d: %w", idx, err)
		}

		ticket := u.engine.EnqueueWrite(int64(fblockOffset(u.geo, idx)), buf)
		res, werr := ticket.Wait()
		if werr != nil {
			u.health.RecordWrite(true)
			return [16]byte{}, fmt.Errorf("storage: recorder: write fblock %d: %w", idx, werr)
		}
		if res.Corrupted {
			u.health.RecordWrite(true)
			u.notify.Publish(Event{Name: EventFblockWriteFailed, Index: idx, UUID: uuid})
			err := u.mgr.MarkBad(idx)
			if err != nil {
				return [16]byte{}, err
			}
			continue // retry: positions/uuid/begin/end/content/toc unchanged
		}

		u.health.RecordWrite(false)
		err = u.mgr.CompleteWrite(idx, uuid, begin, end)
		if err != nil {
			return [16]byte{}, err
		}
		u.saveSSDCatalogBestEffort(u.mgr.Snapshot(), SSDCatalogMeta{WriteSequence: seq, CatalogTime: now, Cursor: idx})
		u.notify.Publish(Event{Name: EventFblockWriteCompleted, Index: idx, UUID: uuid})
		u.health.CheckBadRatio(u.mgr)
		return uuid, nil
	}
}

// saveSSDCatalogBestEffort persists cat to the SSD mirror if one is
// configured. Failures are non-fatal (ADR-007: the catalog is an optional
// optimization, Storage stays fully functional without it) but are
// surfaced as a storage.alert rather than silently swallowed.
func (u *Unit) saveSSDCatalogBestEffort(cat *fblock.Catalog, meta SSDCatalogMeta) {
	if u.catalogPath == "" {
		return
	}
	err := SaveSSDCatalog(u.catalogPath, cat, meta)
	if err != nil {
		u.notify.Publish(Event{Name: EventStorageAlert, Severity: "warning", Reason: AlertSSDCatalogWriteFailed})
	}
}
