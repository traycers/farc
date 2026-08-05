package segmentcache

// Key identifies one cached segment file: a channel's view of a fcontainer
// (Channel, StorageID, UUID) plus which piece of it — the init segment
// (SegIndex == -1) or one media segment (SegIndex >= 0), matching
// internal/hlsapi's routes ("/segments/{channel}/{storage}/{uuid}/init.mp4"
// and "/segments/{channel}/{storage}/{uuid}/{n}/seg.m4s").
//
// Channel is part of the key, not just the UUID, because one fcontainer
// routinely holds several channels' interleaved data at once (ADR-014: the
// normal operating mode, not an edge case) — each with its own codec config
// and segment grid. Without Channel here, two channels sharing a fcontainer
// would collide on the same cache entry and one would silently be served
// the other's init/media segment.
//
// The same (channel,storage,uuid,segIndex) key is reachable from any
// playback window that happens to cover this fcontainer (ADR-019's segment
// grid is a per-fcontainer, not per-request, property), which is exactly
// the caching payoff a per-fcontainer grid is meant to buy.
type Key struct {
	Channel   uint16
	StorageID string
	UUID      [16]byte
	SegIndex  int
}

// InitKey builds the key for a fcontainer's init segment, as seen by channel.
func InitKey(channel uint16, storageID string, uuid [16]byte) Key {
	return Key{Channel: channel, StorageID: storageID, UUID: uuid, SegIndex: -1}
}

// MediaKey builds the key for one media segment within a fcontainer, as seen
// by channel.
func MediaKey(channel uint16, storageID string, uuid [16]byte, segIndex int) Key {
	return Key{Channel: channel, StorageID: storageID, UUID: uuid, SegIndex: segIndex}
}

// IsInit reports whether k identifies an init segment rather than a media
// segment.
func (k Key) IsInit() bool { return k.SegIndex < 0 }
