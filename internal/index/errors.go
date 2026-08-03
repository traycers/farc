package index

import "errors"

var (
	// ErrNoSpace is the "no free fblocks" fault (docs/docs/archive/
	// 04-storage-operations.md §6.4): select_next_index found neither an
	// uninitialized fblock nor (in cyclic mode) a ready one past retention.
	ErrNoSpace = errors.New("index: no free fblocks (NO_SPACE)")

	// ErrChannelRegistryFull is the alert raised when a new channel number
	// needs a compact position but none is free (docs/docs/archive/
	// 04-storage-operations.md §7.1.1, ADR-014).
	ErrChannelRegistryFull = errors.New("index: channel registry full (channel_registry_full)")

	// ErrProtectedRequiresReady: protected may only be set on a Ready fblock
	// (docs/docs/archive/00-requirements.md §4.6).
	ErrProtectedRequiresReady = errors.New("index: protected flag only applies to ready fblocks")

	ErrIndexOutOfRange = errors.New("index: fblock index out of range")
)
