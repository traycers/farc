package segmentcache

// Key identifies one cached segment file: a fcontainer (StorageID, UUID)
// plus which piece of it — the init segment (SegIndex == -1) or one media
// segment (SegIndex >= 0), matching internal/hlsapi's routes
// ("/segments/{storage}/{uuid}/init.mp4" and
// "/segments/{storage}/{uuid}/{n}/seg.m4s").
//
// The same (storage,uuid,segIndex) key is reachable from any playback
// window that happens to cover this fcontainer (ADR-019's segment grid is a
// per-fcontainer, not per-request, property), which is exactly the caching
// payoff a per-fcontainer grid is meant to buy.
type Key struct {
	StorageID string
	UUID      [16]byte
	SegIndex  int
}

// InitKey builds the key for a fcontainer's init segment.
func InitKey(storageID string, uuid [16]byte) Key {
	return Key{StorageID: storageID, UUID: uuid, SegIndex: -1}
}

// MediaKey builds the key for one media segment within a fcontainer.
func MediaKey(storageID string, uuid [16]byte, segIndex int) Key {
	return Key{StorageID: storageID, UUID: uuid, SegIndex: segIndex}
}

// IsInit reports whether k identifies an init segment rather than a media
// segment.
func (k Key) IsInit() bool { return k.SegIndex < 0 }
