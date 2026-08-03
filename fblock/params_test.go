package fblock

import "testing"

func TestParamsRoundTrip(t *testing.T) {
	p := Params{
		FchunkSize:        4194304,
		WriteMode:         WriteModeCyclic,
		Retention:         Retention{Days: 30},
		MinContainerShare: 0.7,
	}
	buf, err := EncodeParams(p)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}
	got, err := DecodeParams(buf)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}
	if got.FchunkSize != p.FchunkSize || got.WriteMode != p.WriteMode || got.Retention.Days != p.Retention.Days {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, p)
	}
	// read_chunk_size defaults to fchunk_size when absent.
	if got.ReadChunkSize != p.FchunkSize {
		t.Errorf("ReadChunkSize default = %d, want %d", got.ReadChunkSize, p.FchunkSize)
	}
}

func TestParamsExampleFromDocs(t *testing.T) {
	// The literal example JSON from 03-storage-format.md §5.2.
	raw := []byte(`{
  "fchunk_size": 4194304,
  "write_mode": "cyclic",
  "retention": {
    "days": 30
  },
  "min_container_share": 0.7
}`)
	p, err := DecodeParams(raw)
	if err != nil {
		t.Fatalf("DecodeParams(doc example): %v", err)
	}
	if p.FchunkSize != 4194304 || p.WriteMode != "cyclic" || p.Retention.Days != 30 || p.MinContainerShare != 0.7 {
		t.Fatalf("unexpected decode: %+v", p)
	}
}

func TestParamsUnknownKeysIgnored(t *testing.T) {
	raw := []byte(`{"fchunk_size":1,"write_mode":"cyclic","some_future_field":"xyz"}`)
	if _, err := DecodeParams(raw); err != nil {
		t.Fatalf("unknown key should be ignored, got error: %v", err)
	}
}

func TestParamsMissingRequiredFields(t *testing.T) {
	cases := []string{
		`{"write_mode":"cyclic"}`, // missing fchunk_size
		`{"fchunk_size":1}`,       // missing write_mode
	}
	for _, raw := range cases {
		if _, err := DecodeParams([]byte(raw)); err == nil {
			t.Errorf("expected error decoding %q", raw)
		}
	}
}

func TestParamsInvalidWriteMode(t *testing.T) {
	err := ValidateParams(Params{FchunkSize: 1, WriteMode: "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid write_mode")
	}
}

func TestParamsInvalidShare(t *testing.T) {
	err := ValidateParams(Params{FchunkSize: 1, WriteMode: WriteModeCyclic, MinContainerShare: 1.5})
	if err == nil {
		t.Fatal("expected error for out-of-range min_container_share")
	}
}
