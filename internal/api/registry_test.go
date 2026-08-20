package api

import (
	"testing"

	"github.com/traycers/farc/internal/storage"
)

func TestStorageRegistry_RegisterGetList(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)

	err := reg.Register("s1", u, "/tmp/s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := reg.Get("s1")
	if !ok || got != u {
		t.Fatalf("Get(s1) = %v, %v; want %v, true", got, ok, u)
	}

	list := reg.List()
	if len(list) != 1 || list[0].ID != "s1" || list[0].Path != "/tmp/s1.img" {
		t.Fatalf("List = %+v", list)
	}
	if list[0].Geometry != u.Geometry() {
		t.Fatalf("List geometry = %+v, want %+v", list[0].Geometry, u.Geometry())
	}
}

func TestStorageRegistry_DuplicateIDRejected(t *testing.T) {
	reg := NewStorageRegistry()
	u1 := newTestUnit(t)
	u2 := newTestUnit(t)

	err := reg.Register("dup", u1, "a", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register("dup", u2, "b", "", storage.PoolTuning{}); err == nil {
		t.Fatalf("second Register with same id: want error, got nil")
	}
}

// TestStorageRegistry_List_ReturnsCachedPoolNotTheLiveUnits guards the
// registry's own cached pool field, independent of the Unit's actual
// PoolTuning() -- issue 03's PATCH-visible-immediately-in-GET behavior
// depends on List() reading e.pool, not e.unit.PoolTuning(). u itself opens
// with the zero-value (resolves to storage.DefaultPoolTuning()), while the
// registry is told a different value -- List() must report the registry's.
func TestStorageRegistry_List_ReturnsCachedPoolNotTheLiveUnits(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if u.PoolTuning() == (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("test setup invalid: u's actual PoolTuning already matches the registered value")
	}

	err := reg.Register("s1", u, "/tmp/s1.img", "", storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	list := reg.List()
	if len(list) != 1 || list[0].Pool != (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("List()[0].Pool = %+v, want the cached {8 4 8}, not the unit's own %+v", list[0].Pool, u.PoolTuning())
	}
}

func TestStorageRegistry_SetPoolTuning_UpdatesListInPlace(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "/tmp/s1.img", "", storage.PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if !reg.SetPoolTuning("s1", storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("SetPoolTuning: want true for a registered id")
	}
	if got := reg.List()[0].Pool; got != (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("List()[0].Pool after SetPoolTuning = %+v, want {8 4 8}", got)
	}
}

func TestStorageRegistry_SetPoolTuning_UnknownIDReturnsFalse(t *testing.T) {
	reg := NewStorageRegistry()
	if reg.SetPoolTuning("nope", storage.PoolTuning{}) {
		t.Fatalf("SetPoolTuning(nope): want false")
	}
}

func TestStorageRegistry_GetUnknown(t *testing.T) {
	reg := NewStorageRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Fatalf("Get(nope) = true, want false")
	}
}
