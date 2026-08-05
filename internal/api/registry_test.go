package api

import "testing"

func TestStorageRegistry_RegisterGetList(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)

	if err := reg.Register("s1", u, "/tmp/s1.img", ""); err != nil {
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

	if err := reg.Register("dup", u1, "a", ""); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register("dup", u2, "b", ""); err == nil {
		t.Fatalf("second Register with same id: want error, got nil")
	}
}

func TestStorageRegistry_GetUnknown(t *testing.T) {
	reg := NewStorageRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Fatalf("Get(nope) = true, want false")
	}
}
