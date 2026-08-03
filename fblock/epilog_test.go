package fblock

import (
	"errors"
	"testing"
)

func TestEpilogRoundTrip(t *testing.T) {
	e := Epilog{CRC32Content: 111, CRC32TOC: 222, TOCSize: 4096}
	buf := EncodeEpilog(e)
	if len(buf) != EpilogSize {
		t.Fatalf("encoded len = %d, want %d", len(buf), EpilogSize)
	}
	got, err := DecodeEpilog(buf)
	if err != nil {
		t.Fatalf("DecodeEpilog: %v", err)
	}
	if got != e {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, e)
	}
}

func TestEpilogIncompleteWrite(t *testing.T) {
	buf := make([]byte, EpilogSize) // power loss before epilog was written
	_, err := DecodeEpilog(buf)
	if !errors.Is(err, ErrIncompleteWrite) {
		t.Fatalf("got err %v, want ErrIncompleteWrite", err)
	}
}

// TestEpilogDiagnosisTable exercises every row of the write-completion
// diagnosis table in docs/docs/archive/03-storage-format.md §9.1.
func TestEpilogDiagnosisTable(t *testing.T) {
	cases := []struct {
		name string
		d    EpilogDiagnosis
		want WriteCompletion
	}{
		{"complete", EpilogDiagnosis{true, true, true}, WriteComplete},
		{"toc corrupted", EpilogDiagnosis{true, true, false}, WriteTOCCorrupted},
		{"content corrupted", EpilogDiagnosis{true, false, false}, WriteContentCorrupted},
		{"content corrupted, toc irrelevant", EpilogDiagnosis{true, false, true}, WriteContentCorrupted},
		{"incomplete", EpilogDiagnosis{false, false, false}, WriteIncomplete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.d.Status(); got != c.want {
				t.Errorf("Status() = %v, want %v", got, c.want)
			}
		})
	}
}
