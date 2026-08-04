package api

import (
	"encoding/hex"
	"net/http"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
)

// subscribeMessage is the one message a client sends right after connecting
// (the sketch's "one subscribe message on connect"). Want/Channels empty
// means "no filter" (everything for that dimension).
type subscribeMessage struct {
	Storage  string   `json:"storage"`
	Want     []string `json:"want"`
	Channels []uint16 `json:"channels"`
}

// pushMessage is one WS frame pushed to a subscribed client — a compact,
// JSON-friendly mirror of storage.Event.
type pushMessage struct {
	Type     string `json:"type"` // always "event" in v1 (see api.go's package doc: no toc push type yet)
	Name     string `json:"name"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// EventPushServer is the WS half of Phase 10's minimal API: one endpoint,
// subscribe-on-connect, forwarding a Storage's NotificationBus events for
// the lifetime of the connection. No reconnect catch-up in v1 (the sketch's
// own explicit deferral) — a client that misses events while disconnected
// gets nothing for that gap; TOC catch-up for actual data is GET
// .../resolve instead.
type EventPushServer struct {
	reg      *StorageRegistry
	upgrader websocket.Upgrader
}

// NewEventPushServer creates a WS push server over reg. CheckOrigin always
// allows: this is an internal operator/consumer API, not a browser-facing
// public endpoint, so no same-origin policy applies.
func NewEventPushServer(reg *StorageRegistry) *EventPushServer {
	return &EventPushServer{
		reg:      reg,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

// ServeHTTP upgrades the connection, reads exactly one subscribe message,
// then forwards matching events until the client disconnects.
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

	// Detect client disconnect (e.g. a close frame or a broken pipe) --
	// v1 never expects a second client message, so any read completing at
	// all (message or error) means the session is over.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

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
