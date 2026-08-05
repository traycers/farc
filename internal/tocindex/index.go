// Package tocindex maintains hls_server's in-memory ChannelIndex: per
// channel, a time-ordered set of fcontainer records with their already
// decoded TOC (ADR-018). EventSubscriber keeps it current from farcd's push
// events, falling back to a bootstrap resolve only on (re)connect — the
// index is what lets playlist/segment building read a fully local structure
// on hls_server's hot path instead of round-tripping to farcd per request.
package tocindex

import (
	"sort"
	"sync"

	"traycers/farc/toc"
)

// Record is one fcontainer entry in a channel's index: the fcontainer's
// identity (StorageID, UUID), the channel's own time bounds within it
// (Begin/End — not the fblock-level bounds, see indexContainer), and its
// already decoded TOC, ready for playlist/segment building without a
// further farcd round trip.
type Record struct {
	UUID      [16]byte
	StorageID string
	Begin     uint64
	End       uint64
	Columns   *toc.Columns
}

// ChannelIndex is one channel's set of known fcontainer records, keyed by
// UUID so a repeated Insert (e.g. re-bootstrap after reconnect) or a
// fblock.deleted Remove is idempotent.
type ChannelIndex struct {
	mu      sync.RWMutex
	records map[[16]byte]Record
}

func newChannelIndex() *ChannelIndex {
	return &ChannelIndex{records: make(map[[16]byte]Record)}
}

// Insert adds or replaces rec.
func (c *ChannelIndex) Insert(rec Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[rec.UUID] = rec
}

// Remove drops uuid, if present. A no-op for a uuid this channel never held
// (e.g. a fblock.deleted event for a container that carried a different
// channel).
func (c *ChannelIndex) Remove(uuid [16]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, uuid)
}

// Lookup returns the record for uuid, if indexed.
func (c *ChannelIndex) Lookup(uuid [16]byte) (Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.records[uuid]
	return rec, ok
}

// Records returns every record whose [Begin,End] overlaps [t1,t2], sorted
// ascending by Begin — the exact shape playlist.Build needs to walk a
// playback window's fcontainers in order (ADR-019).
func (c *ChannelIndex) Records(t1, t2 uint64) []Record {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Record, 0, len(c.records))
	for _, rec := range c.records {
		if rec.End < t1 || rec.Begin > t2 {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Begin < out[j].Begin })
	return out
}

// All returns every indexed record, sorted ascending by Begin.
func (c *ChannelIndex) All() []Record {
	return c.Records(0, ^uint64(0))
}

// Index holds one ChannelIndex per channel hls_server is configured to
// serve, created lazily on first access.
type Index struct {
	mu       sync.Mutex
	channels map[uint16]*ChannelIndex
}

// NewIndex creates an empty Index.
func NewIndex() *Index {
	return &Index{channels: make(map[uint16]*ChannelIndex)}
}

// Channel returns channel's ChannelIndex, creating it on first access.
func (idx *Index) Channel(channel uint16) *ChannelIndex {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	ci, ok := idx.channels[channel]
	if !ok {
		ci = newChannelIndex()
		idx.channels[channel] = ci
	}
	return ci
}

// Remove drops channel's entire ChannelIndex, so a later Channel(channel)
// call starts empty rather than serving stale records left over from
// before a live storage reassignment or removal (internal/hlsd's
// reconciliation, ADR-021) -- previously only a full process restart threw
// the whole Index away, which always cleared this implicitly.
func (idx *Index) Remove(channel uint16) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.channels, channel)
}
