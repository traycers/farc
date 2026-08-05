package api

import (
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
)

// subscribeMessage is the one message a client sends right after connecting
// (the sketch's "one subscribe message on connect"). Want/Channels empty
// means "no filter" (everything for that dimension). Storage == "" means a
// "global" subscription — channel-lifecycle events only, not tied to any
// one Storage's NotificationBus (see ServeHTTP/serveGlobal).
type subscribeMessage struct {
	Storage  string   `json:"storage"`
	Want     []string `json:"want"`
	Channels []uint16 `json:"channels"`
}

// pushMessage is one WS frame pushed to a subscribed client — a compact,
// JSON-friendly mirror of storage.Event, or (Channel/Storage set instead of
// Index/UUID) a ChannelEvent from a global subscription.
type pushMessage struct {
	Type     string `json:"type"` // always "event" in v1 (see api.go's package doc: no toc push type yet)
	Name     string `json:"name"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Channel  uint16 `json:"channel,omitempty"`
	Storage  string `json:"storage,omitempty"`
}

// EventChannelCreated/EventChannelRemoved are ChannelEvent's Name values —
// the "global" (storage-less) counterpart to storage.Event's fblock-scoped
// names, published by internal/farcd whenever POST/PUT/DELETE
// /channels(/{id}) successfully commits (see PublishChannelEvent).
const (
	EventChannelCreated = "channel.created"
	EventChannelRemoved = "channel.removed"
)

// ChannelEvent is one channel-lifecycle event, delivered to every "global"
// subscriber (subscribeMessage.Storage == "") regardless of which Storage
// the channel is on — channel lifecycle isn't a per-storage concept the way
// fblock.* events are, so it doesn't go through any single Storage's
// NotificationBus.
type ChannelEvent struct {
	Name    string
	Channel uint16
	Storage string
}

// EventPushServer is the WS half of Phase 10's minimal API: one endpoint,
// subscribe-on-connect, forwarding a Storage's NotificationBus events (or,
// for a global subscription, channel-lifecycle events) for the lifetime of
// the connection. No reconnect catch-up in v1 (the sketch's own explicit
// deferral) — a client that misses events while disconnected gets nothing
// for that gap; TOC catch-up for actual data is GET .../resolve instead
// (channel-lifecycle catch-up is internal/hlsd's own periodic GET /channels
// re-list, not this package's concern).
type EventPushServer struct {
	reg      *StorageRegistry
	upgrader websocket.Upgrader

	mu         sync.Mutex
	globalSubs map[chan ChannelEvent]struct{}
}

// NewEventPushServer creates a WS push server over reg. CheckOrigin always
// allows: this is an internal operator/consumer API, not a browser-facing
// public endpoint, so no same-origin policy applies.
func NewEventPushServer(reg *StorageRegistry) *EventPushServer {
	return &EventPushServer{
		reg:        reg,
		upgrader:   websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		globalSubs: make(map[chan ChannelEvent]struct{}),
	}
}

// watchDisconnect starts a goroutine that closes the returned channel as
// soon as the client disconnects (a message, a close frame, or a read
// error) — v1 never expects a second client message after the initial
// subscribe, so any completed read at all means the session is over.
// Shared by ServeHTTP's per-storage loop and serveGlobal.
func watchDisconnect(conn *websocket.Conn) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()
	return closed
}

// ServeHTTP upgrades the connection, reads exactly one subscribe message,
// then forwards matching events until the client disconnects. Storage == ""
// is a global (channel-lifecycle-only) subscription, handled by
// serveGlobal instead of the per-storage NotificationBus path below.
func (p *EventPushServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the HTTP error response
	}
	defer conn.Close()

	var sub subscribeMessage
	if err := conn.ReadJSON(&sub); err != nil {
		return
	}

	if sub.Storage == "" {
		p.serveGlobal(conn, sub)
		return
	}

	unit, ok := p.reg.Get(sub.Storage)
	if !ok {
		_ = conn.WriteJSON(map[string]string{"error": "unknown storage " + sub.Storage})
		return
	}
	want := make(map[string]bool, len(sub.Want))
	for _, n := range sub.Want {
		want[n] = true
	}

	events := unit.Notify().Subscribe(64)
	defer unit.Notify().Unsubscribe(events)

	closed := watchDisconnect(conn)

	for {
		select {
		case <-closed:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !matchesSubscription(unit, ev, want, sub.Channels) {
				continue
			}
			msg := pushMessage{Type: "event", Name: ev.Name, Index: ev.Index, Severity: ev.Severity, Reason: ev.Reason}
			if ev.UUID != ([16]byte{}) {
				msg.UUID = hex.EncodeToString(ev.UUID[:])
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}
}

// serveGlobal implements ServeHTTP's sub.Storage == "" branch: no
// NotificationBus, no channel-bitmap fblock filtering (that machinery is
// storage-scoped by construction) — just the fixed channel-lifecycle
// vocabulary (EventChannelCreated/EventChannelRemoved), filtered only by
// sub.Want (empty means all, same convention as the per-storage path).
func (p *EventPushServer) serveGlobal(conn *websocket.Conn, sub subscribeMessage) {
	want := make(map[string]bool, len(sub.Want))
	for _, n := range sub.Want {
		want[n] = true
	}

	events := make(chan ChannelEvent, 64)
	p.mu.Lock()
	p.globalSubs[events] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.globalSubs, events)
		p.mu.Unlock()
	}()

	closed := watchDisconnect(conn)

	for {
		select {
		case <-closed:
			return
		case ev := <-events:
			if len(want) > 0 && !want[ev.Name] {
				continue
			}
			msg := pushMessage{Type: "event", Name: ev.Name, Channel: ev.Channel, Storage: ev.Storage}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}
}

// PublishChannelEvent fans evt out to every current global subscriber,
// non-blocking — a slow subscriber whose buffer is full drops the event
// rather than stalling the caller (internal/farcd's persist hooks), mirroring
// storage.NotificationBus.Publish's own drop-if-slow policy. A dropped event
// is not retried by this package; internal/hlsd's periodic re-list against
// GET /channels bounds how stale that can leave a subscriber.
func (p *EventPushServer) PublishChannelEvent(evt ChannelEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.globalSubs {
		select {
		case ch <- evt:
		default:
		}
	}
}

// matchesSubscription applies both subscription filters: want (event name,
// exact match against storage.Event* names) and channels (approximate --
// see api.go's package doc on why bitmap-based fblock filtering can only be
// a "might contain" match, not an exact one).
func matchesSubscription(unit *storage.Unit, ev storage.Event, want map[string]bool, channels []uint16) bool {
	if len(want) > 0 && !want[ev.Name] {
		return false
	}
	if len(channels) == 0 {
		return true
	}
	if ev.Name == storage.EventStorageAlert {
		return true // storage-level, not channel-scoped
	}
	snap := unit.Index().Snapshot()
	if ev.Index >= snap.N {
		return true // no fblock context to filter against (shouldn't normally happen)
	}
	for _, ch := range channels {
		if pos, ok := unit.Index().ChannelPos(ch); ok && snap.ChannelBit(ev.Index, pos) {
			return true
		}
	}
	return false
}
