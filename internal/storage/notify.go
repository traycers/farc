package storage

import "sync"

// Event names (docs/docs/archive/00-requirements.md §4.9, 02-storage.md
// §4.2.6). NotificationBus is "a local event bus for one Storage" — it does
// not retain history; a missed subscriber misses the event (§4.2.6:
// "ретрансляция пропущенных событий — забота EventPushServer", a Phase 10
// concern outside this package).
const (
	EventFblockWriteStarted   = "fblock.write.started"
	EventFblockWriteCompleted = "fblock.write.completed"
	EventFblockWriteFailed    = "fblock.write.failed"
	EventFblockDeleted        = "fblock.deleted"
	EventStorageAlert         = "storage.alert"
)

// Alert reasons (docs/docs/archive/04-storage-operations.md §6.4, §7.1.1;
// 02-storage.md §4.2.7's own bad-ratio example).
const (
	AlertNoFreeFblocks         = "no_free_fblocks"
	AlertChannelRegistryFull   = "channel_registry_full"
	AlertBadRatioExceeded      = "bad_ratio_exceeded"
	AlertSSDCatalogWriteFailed = "ssd_catalog_write_failed"
)

// Event is one NotificationBus message.
type Event struct {
	Name     string
	Index    uint32 // physical fblock index, when applicable
	UUID     [16]byte
	Severity string // "critical", set for storage.alert; empty otherwise
	Reason   string // e.g. AlertNoFreeFblocks; empty for non-alert events
}

// NotificationBus is a minimal local pub/sub for one Storage
// (docs/docs/archive/02-storage.md §4.2.6). Subscribers receive events via
// a buffered channel; a slow subscriber that lets its channel fill simply
// misses subsequent events rather than blocking Recorder/Reader — matching
// "does not retain history, doesn't replay missed events".
type NotificationBus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// NewNotificationBus creates an empty bus.
func NewNotificationBus() *NotificationBus {
	return &NotificationBus{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new listener with the given channel buffer size and
// returns it. Call Unsubscribe when done.
func (b *NotificationBus) Subscribe(buffer int) chan Event {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes ch.
func (b *NotificationBus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Publish sends ev to every current subscriber, non-blocking — a
// subscriber whose buffer is full drops the event rather than stalling the
// publisher (Recorder/Reader must never block on a slow listener).
func (b *NotificationBus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
