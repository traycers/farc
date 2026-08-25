package fblock

// Magic markers, all 8 bytes, per docs/docs/archive/03-storage-format.md.
// MagicCatalog/MagicContent/MagicTOC/MagicEpilog were v1.0's own per-section
// magics (fblock/header.go, epilog.go, since removed) — v2.0's TLV nodes
// (fblock/v2/node.go) define their own independent per-type magic_start
// values instead, so only the two magics still shared across both write
// paths remain here.
var (
	MagicProlog = [8]byte{'F', 'A', 'R', 'C', 'P', 'R', 'O', 'L'}

	// MagicTrailer marks the transient, in_progress-only "current live end
	// of the content stream" (ADR-017's combined data+magic write) —
	// present only while a fblock is in_progress, relocated forward on
	// every periodic-flush trigger, never present in a closed ready fblock.
	MagicTrailer = [8]byte{'F', 'A', 'R', 'C', 'T', 'R', 'L', 'R'}
)
