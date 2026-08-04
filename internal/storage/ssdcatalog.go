package storage

import (
	"encoding/binary"
	"fmt"
	"os"

	"traycers/farc/fblock"
)

// SSD catalog envelope (ADR-007). 03-storage-format.md never actually pins
// this file's own binary layout — ADR-007 only says "массив заголовков
// всех фблоков... или эквивалентная структура массивов" and defers the
// concrete layout to that doc, which doesn't define one. This is therefore
// this package's own gap-fill: reuse fblock.Catalog's already-specified SoA
// encoding verbatim for the payload (satisfying ADR-007's "эквивалентная
// структура" requirement without inventing a second format), wrapped in a
// small envelope carrying just enough to detect corruption/staleness and to
// recover `cursor` on restart.
//
// Cursor is a second gap-fill: 04-storage-operations.md §6.1 says cursor
// is "recovered as the index of the fblock with maximum write_sequence",
// but write_sequence is a per-fblock fixed-prolog field, never part of the
// catalog SoA (03-storage-format.md §6.2) — a catalog loaded from the SSD
// mirror alone has no per-fblock write_sequence to compare. This envelope
// therefore also carries Cursor directly (the physical index Recorder just
// wrote), which path-2's header scan derives independently by literally
// comparing per-fblock write_sequence.
const (
	ssdMagicSize  = 8
	ssdHeaderSize = ssdMagicSize + 8 + 8 + 4 + 4 // magic + write_sequence + catalog_time + cursor + crc32
	ssdCRCOffset  = ssdMagicSize + 8 + 8 + 4
)

var ssdMagic = [8]byte{'F', 'A', 'R', 'C', 'S', 'S', 'D', 'C'}

// ErrSSDCatalogInvalid is returned by LoadSSDCatalog when the magic or
// checksum doesn't check out — the documented trigger for falling back to
// scanning the main disk (docs/docs/archive/04-storage-operations.md §4.1
// step 5).
var ErrSSDCatalogInvalid = fmt.Errorf("storage: SSD catalog missing or invalid")

// SSDCatalogMeta is the envelope's fixed metadata alongside the catalog
// payload itself.
type SSDCatalogMeta struct {
	WriteSequence uint64 // the write_sequence of the fblock this snapshot reflects
	CatalogTime   uint64 // Unix ns, diagnostic only (ADR-008: never used for ordering)
	Cursor        uint32 // physical index of that fblock — IndexManager's resume point
}

// EncodeSSDCatalog serializes cat with its envelope.
func EncodeSSDCatalog(cat *fblock.Catalog, meta SSDCatalogMeta) ([]byte, error) {
	payload, err := fblock.EncodeCatalog(cat)
	if err != nil {
		return nil, fmt.Errorf("storage: encode SSD catalog: %w", err)
	}
	buf := make([]byte, ssdHeaderSize+len(payload))
	copy(buf[0:8], ssdMagic[:])
	binary.LittleEndian.PutUint64(buf[8:16], meta.WriteSequence)
	binary.LittleEndian.PutUint64(buf[16:24], meta.CatalogTime)
	binary.LittleEndian.PutUint32(buf[24:28], meta.Cursor)
	copy(buf[ssdHeaderSize:], payload)
	binary.LittleEndian.PutUint32(buf[ssdCRCOffset:ssdCRCOffset+4], fblock.CRC32(buf[ssdHeaderSize:]))
	return buf, nil
}

// DecodeSSDCatalog parses an envelope produced by EncodeSSDCatalog. c/n are
// the expected MaxChannels/N (known from the Storage's own geometry, itself
// discovered by reading fblock 0 — the SSD catalog is never used to
// bootstrap geometry, only to skip the full-disk header scan).
func DecodeSSDCatalog(buf []byte, c uint16, n uint32) (*fblock.Catalog, SSDCatalogMeta, error) {
	if len(buf) < ssdHeaderSize {
		return nil, SSDCatalogMeta{}, ErrSSDCatalogInvalid
	}
	if [8]byte(buf[0:8]) != ssdMagic {
		return nil, SSDCatalogMeta{}, ErrSSDCatalogInvalid
	}
	meta := SSDCatalogMeta{
		WriteSequence: binary.LittleEndian.Uint64(buf[8:16]),
		CatalogTime:   binary.LittleEndian.Uint64(buf[16:24]),
		Cursor:        binary.LittleEndian.Uint32(buf[24:28]),
	}
	storedCRC := binary.LittleEndian.Uint32(buf[ssdCRCOffset : ssdCRCOffset+4])
	payload := buf[ssdHeaderSize:]
	if fblock.CRC32(payload) != storedCRC {
		return nil, SSDCatalogMeta{}, ErrSSDCatalogInvalid
	}
	cat, err := fblock.DecodeCatalog(payload, c, n)
	if err != nil {
		return nil, SSDCatalogMeta{}, fmt.Errorf("%w: %v", ErrSSDCatalogInvalid, err)
	}
	return cat, meta, nil
}

// SaveSSDCatalog writes cat to path, fsyncing before return (ADR-007's
// update step requires durability).
func SaveSSDCatalog(path string, cat *fblock.Catalog, meta SSDCatalogMeta) error {
	buf, err := EncodeSSDCatalog(cat, meta)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage: open SSD catalog %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("storage: write SSD catalog %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("storage: fsync SSD catalog %s: %w", path, err)
	}
	return nil
}

// LoadSSDCatalog reads and validates the catalog at path. Returns
// ErrSSDCatalogInvalid (wrapped) if the file is absent, truncated, or fails
// its checksum — all of which mean "fall back to scanning the main disk",
// not a hard error.
func LoadSSDCatalog(path string, c uint16, n uint32) (*fblock.Catalog, SSDCatalogMeta, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, SSDCatalogMeta{}, fmt.Errorf("%w: %v", ErrSSDCatalogInvalid, err)
	}
	return DecodeSSDCatalog(buf, c, n)
}
