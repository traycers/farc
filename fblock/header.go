package fblock

import (
	"encoding/binary"
	"fmt"
)

// Header is everything in a fblock before Content: the fixed prolog, JSON
// params, and catalog snapshot (docs/docs/archive/03-storage-format.md §3.2,
// §5-§7).
type Header struct {
	Prolog  FixedProlog
	Params  Params
	Catalog *Catalog
}

// EncodeHeader serializes a full header: fixed prolog + params JSON +
// magic_catalog + catalog + 3×CRC32. h.Prolog.ParamsSize/CatalogEntryCount/
// CatalogSize/MaxChannels are overwritten in place (on *h) to match the
// actual encoded params/catalog before serialization — callers only need to
// set the other fixed-prolog fields (FormatVersion*, WriteSequence,
// CatalogTime, FblockSize), and can read the final sizes back from h
// afterward.
func EncodeHeader(h *Header) ([]byte, error) {
	paramsBuf, err := EncodeParams(h.Params)
	if err != nil {
		return nil, fmt.Errorf("fblock: encode header: %w", err)
	}
	catalogBuf, err := EncodeCatalog(h.Catalog)
	if err != nil {
		return nil, fmt.Errorf("fblock: encode header: %w", err)
	}

	h.Prolog.ParamsSize = uint32(len(paramsBuf))
	h.Prolog.CatalogEntryCount = h.Catalog.N
	h.Prolog.CatalogSize = uint32(len(catalogBuf))
	h.Prolog.MaxChannels = h.Catalog.MaxChannels

	fixedBuf := EncodeFixedProlog(h.Prolog)

	crcFixed := CRC32(fixedBuf)
	crcParams := CRC32(paramsBuf)
	crcCatalog := CRC32(catalogBuf)

	total := len(fixedBuf) + len(paramsBuf) + 8 + len(catalogBuf) + HeaderChecksumsSize
	buf := make([]byte, total)
	off := 0
	off += copy(buf[off:], fixedBuf)
	off += copy(buf[off:], paramsBuf)
	off += copy(buf[off:], MagicCatalog[:])
	off += copy(buf[off:], catalogBuf)
	binary.LittleEndian.PutUint32(buf[off:off+4], crcFixed)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:off+4], crcParams)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:off+4], crcCatalog)

	return buf, nil
}

// DecodeHeader parses as much of buf's header as its checksums allow,
// following the graceful-degradation diagnosis table in docs/docs/archive/
// 03-storage-format.md §7.1. It returns ErrUninitialized (via err) if
// magic_prolog is absent. Otherwise err is nil and the returned Diagnosis
// says which of h.Params/h.Catalog are actually populated — a non-Intact
// diagnosis is not itself an error, it's the documented degraded-read
// outcome the caller (typically ConsistencyCheck) must act on.
func DecodeHeader(buf []byte) (h Header, diag HeaderDiagnosis, err error) {
	prolog, err := DecodeFixedProlog(buf)
	if err != nil {
		return Header{}, HeaderDiagnosis{}, err
	}
	h.Prolog = prolog

	fixedBuf := buf[0:FixedPrologSize]
	diag.FixedValid = true // magic_prolog checked in DecodeFixedProlog; CRC checked below

	offs := ComputeOffsets(prolog.ParamsSize, prolog.CatalogSize)
	checksumsEnd := offs.ChecksumsOffset + HeaderChecksumsSize
	if uint64(len(buf)) < checksumsEnd {
		return Header{}, HeaderDiagnosis{}, fmt.Errorf(
			"fblock: buffer too short (%d bytes) to reach header checksums at offset %d — params_size/catalog_size likely corrupted",
			len(buf), offs.ChecksumsOffset)
	}

	paramsBuf := buf[offs.ParamsOffset : offs.ParamsOffset+uint64(prolog.ParamsSize)]
	catalogMagicOK := [8]byte(buf[offs.MagicCatalogOffset:offs.MagicCatalogOffset+8]) == MagicCatalog
	catalogBuf := buf[offs.CatalogOffset : offs.CatalogOffset+uint64(prolog.CatalogSize)]

	storedCRCFixed := binary.LittleEndian.Uint32(buf[offs.ChecksumsOffset : offs.ChecksumsOffset+4])
	storedCRCParams := binary.LittleEndian.Uint32(buf[offs.ChecksumsOffset+4 : offs.ChecksumsOffset+8])
	storedCRCCatalog := binary.LittleEndian.Uint32(buf[offs.ChecksumsOffset+8 : offs.ChecksumsOffset+12])

	diag.FixedValid = CRC32(fixedBuf) == storedCRCFixed
	diag.ParamsValid = CRC32(paramsBuf) == storedCRCParams
	// magic_catalog mismatch is diagnosed as catalog-invalid without paying
	// for the (potentially huge) CRC32 over garbage bytes — cheaper first
	// step per §6 / §7.1.
	diag.CatalogValid = catalogMagicOK && CRC32(catalogBuf) == storedCRCCatalog

	if !diag.FixedValid {
		// Fixed part itself is untrustworthy; params_size/catalog_size used
		// above to locate everything else may be garbage too. Nothing
		// further can be safely decoded.
		return h, diag, nil
	}
	if diag.ParamsValid {
		if p, perr := DecodeParams(paramsBuf); perr == nil {
			h.Params = p
		} else {
			diag.ParamsValid = false
		}
	}
	if diag.CatalogValid {
		if c, cerr := DecodeCatalog(catalogBuf, prolog.MaxChannels, prolog.CatalogEntryCount); cerr == nil {
			h.Catalog = c
		} else {
			diag.CatalogValid = false
		}
	}
	return h, diag, nil
}
