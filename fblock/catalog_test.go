package fblock

import "testing"

func TestEntrySizeWorkedExample(t *testing.T) {
	// docs/docs/archive/03-storage-format.md §6.2: "при C=256 ... запись
	// каталога — 33 + 32 = 65 байт".
	if got := EntrySize(256); got != 65 {
		t.Errorf("EntrySize(256) = %d, want 65", got)
	}
}

func TestCatalogSizeWorkedExample(t *testing.T) {
	// Same section: "для 1 миллиона фблоков каталог занимает ≈ 65 МБ плюс
	// несущественные 512 байт регистра" (C=256).
	const c = 256
	const n = 1_000_000
	got := CatalogSize(c, n)
	want := uint32(c)*2 + n*65 // 512 + 65,000,000 = 65,000,512
	if got != want {
		t.Fatalf("CatalogSize(256, 1e6) = %d, want %d", got, want)
	}
	if got != 65_000_512 {
		t.Fatalf("CatalogSize(256, 1e6) = %d, want 65000512", got)
	}
}

func TestRowBytesCeiling(t *testing.T) {
	cases := map[uint16]int{0: 0, 1: 1, 8: 1, 9: 2, 256: 32, 65535: 8192}
	for c, want := range cases {
		if got := rowBytes(c); got != want {
			t.Errorf("rowBytes(%d) = %d, want %d", c, got, want)
		}
	}
}

func TestCatalogRoundTrip(t *testing.T) {
	const c, n = 16, 8
	cat := NewCatalog(c, n)
	cat.ChannelRegistry[0] = 42
	cat.ChannelRegistry[1] = 43
	cat.SetState(0, Ready)
	cat.SetState(1, InProgress)
	cat.SetState(2, Bad)
	cat.SetProtected(0, true)
	cat.UUID[0] = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	cat.Begin[0] = 1000
	cat.End[0] = 2000
	cat.SetChannelBit(0, 0, true)
	cat.SetChannelBit(0, 15, true)

	buf, err := EncodeCatalog(cat)
	if err != nil {
		t.Fatalf("EncodeCatalog: %v", err)
	}
	if uint32(len(buf)) != CatalogSize(c, n) {
		t.Fatalf("encoded len %d != CatalogSize %d", len(buf), CatalogSize(c, n))
	}

	got, err := DecodeCatalog(buf, c, n)
	if err != nil {
		t.Fatalf("DecodeCatalog: %v", err)
	}
	if got.State(0) != Ready || !got.Protected(0) {
		t.Errorf("entry 0: state=%v protected=%v, want Ready+protected", got.State(0), got.Protected(0))
	}
	if got.State(1) != InProgress {
		t.Errorf("entry 1: state=%v, want InProgress", got.State(1))
	}
	if got.State(2) != Bad {
		t.Errorf("entry 2: state=%v, want Bad", got.State(2))
	}
	if got.UUID[0] != cat.UUID[0] || got.Begin[0] != 1000 || got.End[0] != 2000 {
		t.Errorf("entry 0 metadata mismatch: %+v", got)
	}
	if !got.ChannelBit(0, 0) || !got.ChannelBit(0, 15) || got.ChannelBit(0, 1) {
		t.Errorf("channel bitmap mismatch for entry 0")
	}
	if got.ChannelRegistry[0] != 42 || got.ChannelRegistry[1] != 43 {
		t.Errorf("channel registry mismatch: %v", got.ChannelRegistry)
	}
}

func TestChannelRegistryZeroIsNeverAllocated(t *testing.T) {
	cat := NewCatalog(4, 1)
	if cat.AllocatedPrefix() != 0 {
		t.Fatalf("fresh catalog: AllocatedPrefix() = %d, want 0", cat.AllocatedPrefix())
	}
	cat.ChannelRegistry[0] = 1
	cat.ChannelRegistry[1] = 2
	if got := cat.AllocatedPrefix(); got != 2 {
		t.Fatalf("AllocatedPrefix() = %d, want 2", got)
	}
}

func TestRefCount(t *testing.T) {
	const c, n = 8, 4
	cat := NewCatalog(c, n)
	cat.SetChannelBit(0, 3, true)
	cat.SetChannelBit(2, 3, true)
	if got := cat.RefCount(3); got != 2 {
		t.Errorf("RefCount(3) = %d, want 2", got)
	}
	if got := cat.RefCount(4); got != 0 {
		t.Errorf("RefCount(4) = %d, want 0 (never set)", got)
	}
}

func TestSetStatePreservesProtectedBit(t *testing.T) {
	cat := NewCatalog(1, 1)
	cat.SetState(0, Ready)
	cat.SetProtected(0, true)
	cat.SetState(0, Bad) // e.g. corruption found on a protected fblock
	if cat.State(0) != Bad {
		t.Errorf("state = %v, want Bad", cat.State(0))
	}
	if !cat.Protected(0) {
		t.Errorf("protected bit should survive a state change")
	}
}

func TestReservedFlagBitsRejected(t *testing.T) {
	cat := NewCatalog(1, 1)
	cat.Flags[0] = 0x80 // a reserved bit set
	if _, err := EncodeCatalog(cat); err == nil {
		t.Fatal("expected error encoding catalog with a set reserved flag bit")
	}
}

func TestDecodeCatalogWrongSize(t *testing.T) {
	if _, err := DecodeCatalog(make([]byte, 10), 16, 8); err == nil {
		t.Fatal("expected error for wrong-size buffer")
	}
}
