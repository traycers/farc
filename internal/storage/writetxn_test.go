package storage

import (
	"path/filepath"
	"testing"

	"github.com/traycers/farc/fblock"
)

func TestUnit_BeginFblockWrite_ReturnsHeaderAndTransitionsIndexToInProgress(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	uuid, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}

	idx, h, err := u.beginFblockWrite(1000, uuid, nil, 900, 950)
	if err != nil {
		t.Fatalf("beginFblockWrite: %v", err)
	}

	if h.Catalog.State(idx) != fblock.InProgress {
		t.Fatalf("Catalog.State(%d) = %v, want InProgress", idx, h.Catalog.State(idx))
	}
	if h.Catalog.UUID[idx] != uuid {
		t.Fatalf("Catalog.UUID[%d] = %x, want %x", idx, h.Catalog.UUID[idx], uuid)
	}
	if h.Catalog.Begin[idx] != 900 || h.Catalog.End[idx] != 950 {
		t.Fatalf("Catalog.Begin/End[%d] = %d/%d, want 900/950", idx, h.Catalog.Begin[idx], h.Catalog.End[idx])
	}
	if h.Prolog.WriteSequence == 0 {
		t.Fatal("Prolog.WriteSequence = 0, want non-zero")
	}
	if h.Prolog.CatalogTime != 1000 {
		t.Fatalf("Prolog.CatalogTime = %d, want 1000", h.Prolog.CatalogTime)
	}

	// The live index must reflect the same transition -- beginFblockWrite
	// isn't just handing back a disconnected snapshot.
	if got := u.Index().Snapshot().State(idx); got != fblock.InProgress {
		t.Fatalf("Index().Snapshot().State(%d) = %v, want InProgress", idx, got)
	}
}

func TestUnit_BeginFblockWrite_PublishesFblockDeletedWhenReusingReadySlot(t *testing.T) {
	dir := t.TempDir()
	geo := Geometry{FblockSize: 8192, N: 1, MaxChannels: 8}
	u := initAndOpen(t, dir, geo, filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	events := u.Notify().Subscribe(8)
	defer u.Notify().Unsubscribe(events)

	firstUUID, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	idx, h, err := u.beginFblockWrite(1, firstUUID, nil, 1, 2)
	if err != nil {
		t.Fatalf("beginFblockWrite (first): %v", err)
	}
	if err := u.completeFblockWrite(idx, firstUUID, 1, 2, h.Prolog.WriteSequence, 2); err != nil {
		t.Fatalf("completeFblockWrite (first): %v", err)
	}
	drainEvents(events) // WriteStarted + WriteCompleted from the first cycle, not under test here

	// N=1: with the only slot now Ready, the second beginFblockWrite has no
	// Uninitialized slot to pick -- it must fall back to reusing this one
	// once past retention (smallParams' Retention.Days = 30).
	const nsPerDay = int64(24 * 60 * 60 * 1_000_000_000)
	farFuture := uint64(2) + uint64(31*nsPerDay)

	secondUUID, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	idx2, _, err := u.beginFblockWrite(farFuture, secondUUID, nil, 100, 200)
	if err != nil {
		t.Fatalf("beginFblockWrite (second): %v", err)
	}
	if idx2 != idx {
		t.Fatalf("idx2 = %d, want reuse of %d (N=1)", idx2, idx)
	}

	got := drainEvents(events)
	if !containsEvent(got, EventFblockWriteStarted, idx, secondUUID) {
		t.Fatalf("events %+v missing fblock.write.started for the new uuid", got)
	}
	if !containsEvent(got, EventFblockDeleted, idx, firstUUID) {
		t.Fatalf("events %+v missing fblock.deleted for the overwritten uuid", got)
	}
}

func TestUnit_BeginFblockWrite_PublishesStorageAlertWhenNoFreeFblocks(t *testing.T) {
	dir := t.TempDir()
	geo := Geometry{FblockSize: 8192, N: 1, MaxChannels: 8}
	u := initAndOpen(t, dir, geo, filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	uuid, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	// Occupies the only slot and leaves it in_progress (never completed) --
	// so the next call has neither an uninitialized nor a ready slot to pick.
	if _, _, err := u.beginFblockWrite(1, uuid, nil, 1, 2); err != nil {
		t.Fatalf("beginFblockWrite (first): %v", err)
	}

	events := u.Notify().Subscribe(8)
	defer u.Notify().Unsubscribe(events)

	otherUUID, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	_, _, err = u.beginFblockWrite(2, otherUUID, nil, 2, 3)
	if err == nil {
		t.Fatal("beginFblockWrite (second) = nil error, want ErrNoSpace")
	}

	got := drainEvents(events)
	found := false
	for _, ev := range got {
		if ev.Name == EventStorageAlert && ev.Severity == "critical" && ev.Reason == AlertNoFreeFblocks {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %+v missing storage.alert/no_free_fblocks", got)
	}
}

func TestUnit_CompleteFblockWrite_TransitionsToReadyAndRecordsHealth(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	uuid, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	idx, h, err := u.beginFblockWrite(10, uuid, nil, 10, 20)
	if err != nil {
		t.Fatalf("beginFblockWrite: %v", err)
	}

	events := u.Notify().Subscribe(8)
	defer u.Notify().Unsubscribe(events)

	writesBefore, failuresBefore, _ := u.Health().Stats()

	if err := u.completeFblockWrite(idx, uuid, 10, 20, h.Prolog.WriteSequence, 30); err != nil {
		t.Fatalf("completeFblockWrite: %v", err)
	}

	if got := u.Index().Snapshot().State(idx); got != fblock.Ready {
		t.Fatalf("State(%d) = %v, want Ready", idx, got)
	}

	writesAfter, failuresAfter, _ := u.Health().Stats()
	if writesAfter != writesBefore+1 {
		t.Fatalf("writes = %d, want %d", writesAfter, writesBefore+1)
	}
	if failuresAfter != failuresBefore {
		t.Fatalf("writeFailures = %d, want unchanged at %d", failuresAfter, failuresBefore)
	}

	got := drainEvents(events)
	if !containsEvent(got, EventFblockWriteCompleted, idx, uuid) {
		t.Fatalf("events %+v missing fblock.write.completed", got)
	}
}

func TestUnit_FailFblockWrite_MarksBadAndRecordsHealth(t *testing.T) {
	dir := t.TempDir()
	u := initAndOpen(t, dir, smallGeometry(), filepath.Join(dir, "storage.catalog"))
	defer u.Close()

	uuid, err := newUUIDv4()
	if err != nil {
		t.Fatalf("newUUIDv4: %v", err)
	}
	idx, _, err := u.beginFblockWrite(10, uuid, nil, 10, 20)
	if err != nil {
		t.Fatalf("beginFblockWrite: %v", err)
	}

	events := u.Notify().Subscribe(8)
	defer u.Notify().Unsubscribe(events)

	_, failuresBefore, _ := u.Health().Stats()

	if err := u.failFblockWrite(idx, uuid); err != nil {
		t.Fatalf("failFblockWrite: %v", err)
	}

	if got := u.Index().Snapshot().State(idx); got != fblock.Bad {
		t.Fatalf("State(%d) = %v, want Bad", idx, got)
	}

	_, failuresAfter, _ := u.Health().Stats()
	if failuresAfter != failuresBefore+1 {
		t.Fatalf("writeFailures = %d, want %d", failuresAfter, failuresBefore+1)
	}

	got := drainEvents(events)
	if !containsEvent(got, EventFblockWriteFailed, idx, uuid) {
		t.Fatalf("events %+v missing fblock.write.failed", got)
	}
}

func drainEvents(ch chan Event) []Event {
	var got []Event
	for {
		select {
		case ev := <-ch:
			got = append(got, ev)
		default:
			return got
		}
	}
}

func containsEvent(events []Event, name string, idx uint32, uuid [16]byte) bool {
	for _, ev := range events {
		if ev.Name == name && ev.Index == idx && ev.UUID == uuid {
			return true
		}
	}
	return false
}
