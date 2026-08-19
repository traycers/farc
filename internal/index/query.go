package index

import "github.com/traycers/farc/fblock"

// ResolveUUID returns the physical index of the Ready fblock holding the
// fcontainer with this UUID, if any (docs/docs/archive/
// 04-storage-operations.md §8.2: IndexManager returns an index only for
// fblocks in state Ready).
func (m *Manager) ResolveUUID(uuid [16]byte) (uint32, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	idx, ok := m.uuidIndex[uuid]
	return idx, ok
}

// Candidates returns, in index order, the physical indices of Ready fblocks
// whose [begin,end] overlaps [t1,t2] and whose channel_bitmap has
// channelNumber's compact position set (docs/docs/archive/
// 00-requirements.md §4.12, ADR-014). This only narrows candidates — exact
// confirmation of data ranges still requires reading each candidate's TOC.
func (m *Manager) Candidates(channelNumber uint16, t1, t2 uint64) []uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pos, ok := m.channelToPos[channelNumber]
	if !ok {
		return nil
	}
	var out []uint32
	for i := uint32(0); i < m.catalog.N; i++ {
		if m.catalog.State(i) != fblock.Ready {
			continue
		}
		if m.catalog.Begin[i] > t2 || m.catalog.End[i] < t1 {
			continue
		}
		if m.catalog.ChannelBit(i, pos) {
			out = append(out, i)
		}
	}
	return out
}
