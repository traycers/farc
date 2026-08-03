package index

// ResolveChannel returns the compact registry position for channelNumber,
// if it has ever been registered, via the in-memory reverse index
// (docs/docs/archive/06-toc-format.md §8 step 1 — the same kind of reverse
// lookup used to resolve a channel node's value).
func (m *Manager) ResolveChannel(channelNumber uint16) (uint16, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pos, ok := m.channelToPos[channelNumber]
	return pos, ok
}

// RegisterChannel resolves a single channelNumber to a compact registry
// position, registering a new one if needed. Equivalent to
// RegisterChannels([]uint16{channelNumber})[0].
func (m *Manager) RegisterChannel(channelNumber uint16) (uint16, error) {
	positions, err := m.RegisterChannels([]uint16{channelNumber})
	if err != nil {
		return 0, err
	}
	return positions[0], nil
}

// RegisterChannels resolves every channel number in channels — the full
// channel list of one buffer being prepared for write — to compact registry
// positions in one atomic pass (docs/docs/archive/04-storage-operations.md
// §7.1.1, ADR-014): reuse a previously-allocated position whose reference
// count is 0, else allocate the lowest never-used position, else fail with
// ErrChannelRegistryFull.
//
// Resolving the whole batch together (rather than one RegisterChannel call
// per channel) matters: a position registered earlier in this same batch
// has RefCount 0 in the real catalog too (its channel_bitmap bit is only
// set later, when Recorder assembles this write's catalog snapshot —
// docs/docs/archive/04-storage-operations.md §7.2 step 3, a separate later
// step from this one). Without batch-local tracking, a naive per-channel
// RefCount==0 reuse check would immediately steal back a position this
// same call just handed to an earlier channel in the list. Positions
// resolved for channels earlier in the batch are therefore excluded from
// the reuse search for channels later in the same batch.
//
// On ErrChannelRegistryFull partway through, channels already resolved
// earlier in the batch keep their (real, valid) registration — the failure
// only refuses this write, per docs/docs/archive/04-storage-operations.md
// §7.1.1 ("алерт channel_registry_full и отказ"); it does not undo prior,
// individually-valid registrations.
func (m *Manager) RegisterChannels(channels []uint16) ([]uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	positions := make([]uint16, len(channels))
	reservedThisBatch := make(map[uint16]bool, len(channels))

	for i, ch := range channels {
		if pos, ok := m.channelToPos[ch]; ok {
			positions[i] = pos
			reservedThisBatch[pos] = true
			continue
		}

		pos, err := m.allocateChannelPositionLocked(ch, reservedThisBatch)
		if err != nil {
			return nil, err
		}
		positions[i] = pos
		reservedThisBatch[pos] = true
	}
	return positions, nil
}

// allocateChannelPositionLocked implements the actual allocation rule.
// Caller must hold m.mu.
func (m *Manager) allocateChannelPositionLocked(channelNumber uint16, excluded map[uint16]bool) (uint16, error) {
	prefix := m.catalog.AllocatedPrefix()
	for pos := 0; pos < prefix; pos++ {
		p := uint16(pos)
		if excluded[p] {
			continue
		}
		if m.catalog.RefCount(p) == 0 {
			if old := m.catalog.ChannelRegistry[pos]; old != 0 {
				delete(m.channelToPos, old)
			}
			m.catalog.ChannelRegistry[pos] = channelNumber
			m.channelToPos[channelNumber] = p
			return p, nil
		}
	}
	if prefix >= int(m.catalog.MaxChannels) {
		return 0, ErrChannelRegistryFull
	}
	pos := uint16(prefix)
	m.catalog.ChannelRegistry[pos] = channelNumber
	m.channelToPos[channelNumber] = pos
	return pos, nil
}
