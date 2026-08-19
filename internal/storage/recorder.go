package storage

import (
	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
)

// WriteFcontainer performs docs/docs/archive/04-storage-operations.md §7 end
// to end for one already-filled fcontainer: it's a thin backward-compat
// wrapper over BeginSegment/Close (the buffer-pool/early-index-assignment/
// periodic-flush path — see segment.go) — no separate code path, this *is*
// that path, just fed an already-fully-built Filler in one shot instead of
// incrementally. now is Unix ns, used for select_next_index's retention
// check and as this write's catalog_time.
func (u *Unit) WriteFcontainer(channels []uint16, begin, end uint64, filler *fcontainer.Filler, now uint64) ([16]byte, error) {
	seg, err := u.beginSegmentWithFiller(channels, filler, begin, end, now)
	if err != nil {
		return [16]byte{}, err
	}
	return seg.Close(now)
}

// beginSegmentWithFiller is BeginSegment's internal variant for a caller
// that already has a fully-built *fcontainer.Filler and its own known
// begin/end (WriteFcontainer's only caller) — segmentImpl wraps that
// Filler directly instead of allocating a fresh one, and its whole tree is
// pushed as this segment's initial content.
func (u *Unit) beginSegmentWithFiller(channels []uint16, filler *fcontainer.Filler, begin, end, now uint64) (*segmentImpl, error) {
	uuid, err := newUUIDv4()
	if err != nil {
		return nil, err
	}
	seg := &segmentImpl{
		unit:      u,
		filler:    filler,
		positions: make(map[uint16]uint16),
		uuid:      uuid,
		begin:     begin,
		end:       end,
		haveFrame: true,
	}
	for _, ch := range channels {
		err := seg.RegisterChannel(ch)
		if err != nil {
			return nil, err
		}
	}
	_, err = u.pool.reserve(seg, now)
	if err != nil {
		return nil, err
	}
	seg.mu.Lock()
	err = seg.pushReadyLocked(now)
	seg.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return seg, nil
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
