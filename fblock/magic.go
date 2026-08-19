package fblock

// Magic markers, all 8 bytes, per docs/docs/archive/03-storage-format.md.
var (
	MagicProlog  = [8]byte{'F', 'A', 'R', 'C', 'P', 'R', 'O', 'L'}
	MagicCatalog = [8]byte{'F', 'A', 'R', 'C', 'C', 'T', 'L', 'G'}
	MagicContent = [8]byte{'F', 'A', 'R', 'C', 'C', 'O', 'N', 'T'}
	MagicTOC     = [8]byte{'F', 'A', 'R', 'C', 'T', 'O', 'C', '0'}
	MagicEpilog  = [8]byte{'F', 'A', 'R', 'C', 'E', 'P', 'I', 'L'}

	// MagicTrailer marks the transient, in_progress-only "current live end
	// of the content stream" (ADR-017's combined data+magic write) —
	// present only while a fblock is in_progress, relocated forward on
	// every periodic-flush trigger, never present in a closed ready fblock.
	MagicTrailer = [8]byte{'F', 'A', 'R', 'C', 'T', 'R', 'L', 'R'}
)
