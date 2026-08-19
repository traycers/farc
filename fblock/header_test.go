package fblock

import "testing"

func sampleHeader() Header {
	cat := NewCatalog(16, 4)
	cat.SetState(0, InProgress)
	cat.SetState(1, Ready)
	cat.UUID[1] = [16]byte{9, 9, 9}
	return Header{
		Prolog: FixedProlog{
			FormatVersionMajor: 1,
			WriteSequence:      7,
			CatalogTime:        555,
			FblockSize:         1 << 20,
		},
		Params: Params{
			FchunkSize:        4 << 20,
			WriteMode:         WriteModeCyclic,
			Retention:         Retention{Days: 30},
			MinContainerShare: 0.7,
		},
		Catalog: cat,
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	h := sampleHeader()
	buf, err := EncodeHeader(&h)
	if err != nil {
		t.Fatalf("EncodeHeader: %v", err)
	}

	// Pad with a fake epilog-sized tail so DecodeHeader's bounds check
	// (which only needs to reach the checksums, well before any content)
	// has a realistic buffer to work with.
	full := make([]byte, len(buf)+64)
	copy(full, buf)

	got, diag, err := DecodeHeader(full)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if diag.Status() != HeaderIntact {
		t.Fatalf("diag.Status() = %v, want HeaderIntact (%+v)", diag.Status(), diag)
	}
	if got.Prolog.WriteSequence != 7 || got.Prolog.CatalogTime != 555 {
		t.Errorf("prolog mismatch: %+v", got.Prolog)
	}
	if got.Params.WriteMode != WriteModeCyclic || got.Params.Retention.Days != 30 {
		t.Errorf("params mismatch: %+v", got.Params)
	}
	if got.Catalog.State(0) != InProgress || got.Catalog.State(1) != Ready {
		t.Errorf("catalog states mismatch: %v", got.Catalog.Flags)
	}
	if got.Catalog.UUID[1] != h.Catalog.UUID[1] {
		t.Errorf("catalog UUID mismatch")
	}
}

// TestHeaderDiagnosisTable exercises every row of the header-checksum
// diagnosis table in docs/docs/archive/03-storage-format.md §7.1, by
// corrupting exactly one section of an otherwise-valid header at a time.
func TestHeaderDiagnosisTable(t *testing.T) {
	h := sampleHeader()
	buf, err := EncodeHeader(&h)
	if err != nil {
		t.Fatalf("EncodeHeader: %v", err)
	}
	offs := ComputeOffsets(h.Prolog.ParamsSize, h.Prolog.CatalogSize, 1)

	flip := func(src []byte, at uint64) []byte {
		out := append([]byte(nil), src...)
		out[at] ^= 0xFF
		return out
	}

	t.Run("intact", func(t *testing.T) {
		_, diag, err := DecodeHeader(buf)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.Status() != HeaderIntact {
			t.Errorf("Status() = %v, want HeaderIntact", diag.Status())
		}
	})

	t.Run("catalog lost", func(t *testing.T) {
		corrupted := flip(buf, offs.CatalogOffset+1)
		_, diag, err := DecodeHeader(corrupted)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.Status() != HeaderCatalogLost {
			t.Errorf("Status() = %v, want HeaderCatalogLost (%+v)", diag.Status(), diag)
		}
	})

	t.Run("params corrupted", func(t *testing.T) {
		corrupted := flip(buf, offs.ParamsOffset+1)
		_, diag, err := DecodeHeader(corrupted)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.Status() != HeaderParamsCorrupted {
			t.Errorf("Status() = %v, want HeaderParamsCorrupted (%+v)", diag.Status(), diag)
		}
	})

	t.Run("only fixed valid", func(t *testing.T) {
		corrupted := flip(buf, offs.ParamsOffset+1)
		corrupted = flip(corrupted, offs.CatalogOffset+1)
		_, diag, err := DecodeHeader(corrupted)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.Status() != HeaderOnlyFixedValid {
			t.Errorf("Status() = %v, want HeaderOnlyFixedValid (%+v)", diag.Status(), diag)
		}
	})

	t.Run("unreadable: fixed part corrupted but magic intact", func(t *testing.T) {
		// Byte 20 is inside catalog_time (offset 24) region... pick byte 30,
		// safely inside the fixed part (0..55) and outside magic_prolog (0..7).
		corrupted := flip(buf, 30)
		_, diag, err := DecodeHeader(corrupted)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.Status() != HeaderUnreadable {
			t.Errorf("Status() = %v, want HeaderUnreadable (%+v)", diag.Status(), diag)
		}
	})

	t.Run("magic_catalog mismatch treated as catalog invalid without CRC", func(t *testing.T) {
		corrupted := flip(buf, offs.MagicCatalogOffset)
		_, diag, err := DecodeHeader(corrupted)
		if err != nil {
			t.Fatalf("DecodeHeader: %v", err)
		}
		if diag.CatalogValid {
			t.Errorf("CatalogValid = true, want false when magic_catalog itself is corrupted")
		}
	})
}
