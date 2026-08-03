// Package index implements IndexManager: the in-memory Storage-wide index
// (catalog snapshot + cursor + channel registry) and the fblock-selection
// algorithm (docs/docs/archive/04-storage-operations.md §6-§8, ADR-014).
//
// Retention checks use plain wall-clock time (`now`, passed by the caller —
// this package never calls time.Now() itself, for testability), per the v1
// simplification of ADR-012: no anomaly protection, see
// docs/docs/archive/adr/012-retention-clock.md.
package index

import (
	"sync"

	"traycers/farc/fblock"
)

const nsPerDay = int64(24 * 60 * 60 * 1_000_000_000)

// Manager is IndexManager: owns the in-memory catalog, write cursor, and
// channel-registry reverse index for one Storage.
type Manager struct {
	mu sync.RWMutex

	catalog       *fblock.Catalog
	cursor        uint32 // last physical index selected for write
	writeMode     string // fblock.WriteModeCyclic or WriteModeFillUntilFull
	retentionDays int64

	uuidIndex    map[[16]byte]uint32 // Ready fblock UUID -> physical index
	channelToPos map[uint16]uint16   // channel number -> compact registry position
}

// New builds a Manager from a loaded catalog. cursor is the physical index
// of the fblock with the maximum write_sequence (docs/docs/archive/
// 04-storage-operations.md §6.1) — the position select_next_index resumes
// from.
func New(catalog *fblock.Catalog, cursor uint32, writeMode string, retentionDays int64) *Manager {
	m := &Manager{
		catalog:       catalog,
		cursor:        cursor,
		writeMode:     writeMode,
		retentionDays: retentionDays,
		uuidIndex:     make(map[[16]byte]uint32),
		channelToPos:  make(map[uint16]uint16),
	}
	for i := uint32(0); i < catalog.N; i++ {
		if catalog.State(i) == fblock.Ready {
			m.uuidIndex[catalog.UUID[i]] = i
		}
	}
	for pos, ch := range catalog.ChannelRegistry {
		if ch != 0 {
			m.channelToPos[ch] = uint16(pos)
		}
	}
	return m
}

// SetWriteMode/SetRetentionDays let the operator mutate these at runtime
// (docs/docs/archive/11-service-composition.md §5.1.2: "изменение
// retention.days"; write_mode is likewise a mutable Storage param).
func (m *Manager) SetWriteMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeMode = mode
}

func (m *Manager) SetRetentionDays(days int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retentionDays = days
}

func (m *Manager) Cursor() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cursor
}

// SelectNextIndex is select_next_index (docs/docs/archive/
// 04-storage-operations.md §6.2), verbatim: priority 1 is the first
// uninitialized fblock walking circularly from cursor+1; priority 2 (only
// when write_mode is cyclic) is the first ready, non-protected fblock past
// retention, same circular order. now is Unix ns wall-clock time.
func (m *Manager) SelectNextIndex(now uint64) (uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	n := m.catalog.N
	if n == 0 {
		return 0, ErrNoSpace
	}

	for i := uint32(0); i < n; i++ {
		idx := (m.cursor + 1 + i) % n
		if m.catalog.State(idx) == fblock.Uninitialized {
			return idx, nil
		}
	}

	if m.writeMode == fblock.WriteModeCyclic {
		retentionNS := uint64(m.retentionDays * nsPerDay)
		for i := uint32(0); i < n; i++ {
			idx := (m.cursor + 1 + i) % n
			if m.catalog.State(idx) == fblock.Ready && !m.catalog.Protected(idx) &&
				now-m.catalog.End[idx] >= retentionNS {
				return idx, nil
			}
		}
	}

	return 0, ErrNoSpace
}

// BeginWrite transitions idx to InProgress and advances the cursor — the
// moment Recorder commits to writing this index (docs/docs/archive/
// 04-storage-operations.md §7.1 step 3). If idx previously held a Ready
// fcontainer, its UUID is dropped from the UUID index (the caller is
// responsible for publishing fblock.deleted, docs/docs/archive/
// 00-requirements.md §4.9 — this method only maintains the in-memory index).
func (m *Manager) BeginWrite(idx uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= m.catalog.N {
		return ErrIndexOutOfRange
	}
	if m.catalog.State(idx) == fblock.Ready {
		delete(m.uuidIndex, m.catalog.UUID[idx])
	}
	m.catalog.SetState(idx, fblock.InProgress)
	m.cursor = idx
	return nil
}

// CompleteWrite transitions idx to Ready with the written fcontainer's
// metadata (docs/docs/archive/04-storage-operations.md §7.3 step 2).
func (m *Manager) CompleteWrite(idx uint32, uuid [16]byte, begin, end uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= m.catalog.N {
		return ErrIndexOutOfRange
	}
	m.catalog.SetState(idx, fblock.Ready)
	m.catalog.UUID[idx] = uuid
	m.catalog.Begin[idx] = begin
	m.catalog.End[idx] = end
	m.uuidIndex[uuid] = idx
	return nil
}

// MarkBad transitions idx to Bad — an unconditional, irreversible-without-
// reinit transition from any state (docs/docs/archive/03-storage-format.md
// §6.3, docs/docs/archive/02-storage.md invariant 9).
func (m *Manager) MarkBad(idx uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= m.catalog.N {
		return ErrIndexOutOfRange
	}
	if m.catalog.State(idx) == fblock.Ready {
		delete(m.uuidIndex, m.catalog.UUID[idx])
	}
	m.catalog.SetState(idx, fblock.Bad)
	return nil
}

// SetProtected sets or clears idx's protected flag. Only valid on a Ready
// fblock (docs/docs/archive/00-requirements.md §4.6).
func (m *Manager) SetProtected(idx uint32, protected bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx >= m.catalog.N {
		return ErrIndexOutOfRange
	}
	if m.catalog.State(idx) != fblock.Ready {
		return ErrProtectedRequiresReady
	}
	m.catalog.SetProtected(idx, protected)
	return nil
}
