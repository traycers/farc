package fblock

// HeaderDiagnosis records which of the three header checksums verified,
// input to the diagnosis table in docs/docs/archive/03-storage-format.md §7.1.
// FixedValid==false implies the other two are meaningless (the offsets used
// to locate params/catalog come from the fixed part itself).
type HeaderDiagnosis struct {
	FixedValid   bool
	ParamsValid  bool
	CatalogValid bool
}

// HeaderStatus is the interpretation of a HeaderDiagnosis, per the §7.1 table.
type HeaderStatus int

const (
	// HeaderIntact: crc32_fixed, crc32_params, crc32_catalog all valid.
	HeaderIntact HeaderStatus = iota
	// HeaderCatalogLost: params available, catalog snapshot lost.
	HeaderCatalogLost
	// HeaderParamsCorrupted: catalog available, params corrupted.
	HeaderParamsCorrupted
	// HeaderOnlyFixedValid: only the fixed part is valid.
	HeaderOnlyFixedValid
	// HeaderUnreadable: crc32_fixed invalid, or magic_prolog absent — caller
	// must inspect magic_prolog separately to distinguish bad from
	// uninitialized (DecodeFixedProlog does this via ErrUninitialized).
	HeaderUnreadable
)

// Status interprets d per docs/docs/archive/03-storage-format.md §7.1.
func (d HeaderDiagnosis) Status() HeaderStatus {
	switch {
	case !d.FixedValid:
		return HeaderUnreadable
	case d.ParamsValid && d.CatalogValid:
		return HeaderIntact
	case d.ParamsValid && !d.CatalogValid:
		return HeaderCatalogLost
	case !d.ParamsValid && d.CatalogValid:
		return HeaderParamsCorrupted
	default:
		return HeaderOnlyFixedValid
	}
}

// EpilogDiagnosis records which write-completion checks verified, input to
// the diagnosis table in docs/docs/archive/03-storage-format.md §9.1. This is
// exactly what ConsistencyCheck (04-storage-operations.md §5) uses to decide
// ready vs bad for the one in_progress fblock found after a restart.
type EpilogDiagnosis struct {
	EpilogValid  bool
	ContentValid bool // crc32_content matches
	TOCValid     bool // crc32_toc matches
}

// WriteCompletion is the interpretation of an EpilogDiagnosis, per the §9.1
// table.
type WriteCompletion int

const (
	// WriteComplete: magic_epilog, crc32_content, crc32_toc all valid ->
	// fblock should be (or become) Ready.
	WriteComplete WriteCompletion = iota
	// WriteTOCCorrupted: epilog+content valid, TOC corrupted (rare — TOC is
	// written before the epilog).
	WriteTOCCorrupted
	// WriteContentCorrupted: epilog valid, content corrupted (physical
	// defect).
	WriteContentCorrupted
	// WriteIncomplete: magic_epilog absent — the write never reached the
	// final stage, fblock was in_progress at the moment of failure.
	WriteIncomplete
)

// Status interprets d per docs/docs/archive/03-storage-format.md §9.1.
// A fblock with a WriteComplete or WriteTOCCorrupted... no: per the table,
// only WriteComplete maps to Ready; every other outcome maps to Bad
// (ConsistencyCheck, 04-storage-operations.md §5.1 step 2).
func (d EpilogDiagnosis) Status() WriteCompletion {
	switch {
	case !d.EpilogValid:
		return WriteIncomplete
	case !d.ContentValid:
		return WriteContentCorrupted
	case !d.TOCValid:
		return WriteTOCCorrupted
	default:
		return WriteComplete
	}
}
