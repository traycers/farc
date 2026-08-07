package api

import (
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
	"traycers/farc/toc"
)

// subscribeMessage is the one message a client sends right after connecting
// (the sketch's "one subscribe message on connect"). Want/Channels empty
// means "no filter" (everything for that dimension). Storage == "" means a
// "global" subscription — channel-lifecycle events only, not tied to any
// one Storage's NotificationBus (see ServeHTTP/serveGlobal). IncludeTOC
// opts a global subscriber into a second "toc" frame (tocPushMessage) right
// after every EventFblockReady it receives -- off by default so ordinary
// Journal/UI clients never pay for a payload they don't use (msm_server is
// the one subscriber that sets it, to compute vaa-blocks without polling
// GET .../toc).
type subscribeMessage struct {
	Storage    string   `json:"storage"`
	Want       []string `json:"want"`
	Channels   []uint16 `json:"channels"`
	IncludeTOC bool     `json:"include_toc"`
}

// pushMessage is one WS frame pushed to a subscribed client — a compact,
// JSON-friendly mirror of storage.Event, or (Channel/Storage set instead of
// Index/UUID) a JournalEvent from a global subscription. Begin/End are only
// meaningful for channel.recording.*/fblock.ready (see JournalEvent).
type pushMessage struct {
	Type     string `json:"type"` // "event" or "toc" (see tocPushMessage)
	Name     string `json:"name"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Channel  uint16 `json:"channel,omitempty"`
	Storage  string `json:"storage,omitempty"`
	Begin    uint64 `json:"begin,omitempty"`
	End      uint64 `json:"end,omitempty"`
}

// tocPushMessage is the WS frame sent right after an EventFblockReady
// pushMessage to a subscriber with IncludeTOC set — the raw TOC section
// bytes for that fblock (the same bytes GET .../fcontainers/{uuid}/toc
// serves, toc.Encode'd), base64'd via encoding/json's normal []byte
// handling. Its own "type" is always "toc", so a client can tell the two
// frames apart without guessing from field presence.
type tocPushMessage struct {
	Type    string `json:"type"`
	Storage string `json:"storage"`
	Index   uint32 `json:"index"`
	UUID    string `json:"uuid"`
	TOC     []byte `json:"toc"`
}

// EventChannelCreated/EventChannelRemoved are JournalEvent's Name values for
// channel lifecycle — the "global" (storage-less) counterpart to
// storage.Event's fblock-scoped names, published by internal/farcd whenever
// POST/PUT/DELETE /channels(/{id}) successfully commits (see Publish).
//
// EventFblockCreated/EventFblockDeleted mirror storage.EventFblockWriteStarted
// /storage.EventFblockDeleted, bridged into the global feed by internal/farcd
// (fblock lifecycle is otherwise only visible via a per-storage subscription).
// "Created" is write-start, including a cyclic reuse of an already-ready
// block — there is no separate "first-ever init" signal in v1.
//
// EventFblockReady mirrors storage.EventFblockWriteCompleted -- the fblock
// has finished writing and is now Ready, with a final [Begin,End] (from the
// Storage's own index/catalog, not recomputed here) and a TOC a subscriber
// can ask to have pushed alongside it (subscribeMessage.IncludeTOC).
//
// EventRecordingStarted/EventRecordingStopped fire on CapturePolicy's actual
// recording-state transition (internal/ingest/policy.go's
// openSegmentLocked/closeSegmentLocked), which can happen without a matching
// command (event policy's Trigger/Tick) — hence these are distinct from
// EventRecordingCommandStart/EventRecordingCommandStop, which fire when
// POST /channels/{id}/recording/start|stop is received, regardless of
// whether it actually changes recording state. Begin/End carry the segment's
// intended start time / actual stop time (CapturePolicy.SetOnRecordingChange's
// own doc comment) -- Begin is set for Started, End for Stopped, never both.
//
// EventTriggerFired fires when POST /channels/{id}/events is received.
const (
	EventChannelCreated        = "channel.created"
	EventChannelRemoved        = "channel.removed"
	EventFblockCreated         = "fblock.created"
	EventFblockReady           = "fblock.ready"
	EventFblockDeleted         = "fblock.deleted"
	EventRecordingStarted      = "channel.recording.started"
	EventRecordingStopped      = "channel.recording.stopped"
	EventRecordingCommandStart = "channel.recording.command.start"
	EventRecordingCommandStop  = "channel.recording.command.stop"
	EventTriggerFired          = "channel.trigger.fired"
)

// JournalEvent is one journal-worthy event, delivered to every "global"
// subscriber (subscribeMessage.Storage == "") regardless of which Storage or
// Channel it concerns — the global feed isn't scoped to any one Storage's
// NotificationBus the way per-storage fblock.* subscriptions are. Not every
// field is set for every Name: Index/UUID/Severity/Reason are only
// meaningful for fblock.* events, Channel only for channel/recording/trigger
// events, Begin/End only for channel.recording.*/fblock.ready (see the Event*
// constants above).
type JournalEvent struct {
	Name     string
	Channel  uint16
	Storage  string
	Index    uint32
	UUID     string
	Severity string
	Reason   string
	Begin    uint64
	End      uint64
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
	globalSubs map[chan JournalEvent]struct{}
}

// NewEventPushServer creates a WS push server over reg. CheckOrigin always
// allows: this is an internal operator/consumer API, not a browser-facing
// public endpoint, so no same-origin policy applies.
func NewEventPushServer(reg *StorageRegistry) *EventPushServer {
	return &EventPushServer{
		reg:        reg,
		upgrader:   websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		globalSubs: make(map[chan JournalEvent]struct{}),
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
			_, _, err := conn.NextReader()
			if err != nil {
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
	defer func() { _ = conn.Close() }()

	var sub subscribeMessage
	err = conn.ReadJSON(&sub)
	if err != nil {
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
			err := conn.WriteJSON(msg)
			if err != nil {
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

	events := make(chan JournalEvent, 64)
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
			msg := pushMessage{
				Type: "event", Name: ev.Name, Channel: ev.Channel, Storage: ev.Storage,
				Index: ev.Index, UUID: ev.UUID, Severity: ev.Severity, Reason: ev.Reason,
				Begin: ev.Begin, End: ev.End,
			}
			err := conn.WriteJSON(msg)
			if err != nil {
				return
			}
			if sub.IncludeTOC && ev.Name == EventFblockReady {
				tocMsg, ok := p.buildTOCPushMessage(ev)
				if ok {
					err := conn.WriteJSON(tocMsg)
					if err != nil {
						return
					}
				}
			}
		}
	}
}

// buildTOCPushMessage reads ev's fblock's TOC section (the same bytes
// GET .../fcontainers/{uuid}/toc serves) for a follow-up "toc" frame --
// false if ev's Storage/UUID can't be resolved to a live fblock anymore
// (e.g. it was already recycled past retention by the time the subscriber
// caught up), in which case the caller just skips the TOC frame rather than
// erroring the whole connection.
func (p *EventPushServer) buildTOCPushMessage(ev JournalEvent) (tocPushMessage, bool) {
	unit, ok := p.reg.Get(ev.Storage)
	if !ok {
		return tocPushMessage{}, false
	}
	uuidBytes, err := hex.DecodeString(ev.UUID)
	if err != nil || len(uuidBytes) != 16 {
		return tocPushMessage{}, false
	}
	var uuid [16]byte
	copy(uuid[:], uuidBytes)
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		return tocPushMessage{}, false
	}
	buf, err := toc.Encode(columns)
	if err != nil {
		return tocPushMessage{}, false
	}
	return tocPushMessage{Type: "toc", Storage: ev.Storage, Index: ev.Index, UUID: ev.UUID, TOC: buf}, true
}

// Publish fans evt out to every current global subscriber, non-blocking — a
// slow subscriber whose buffer is full drops the event rather than stalling
// the caller (internal/farcd's persist hooks, internal/api's command
// handlers), mirroring storage.NotificationBus.Publish's own drop-if-slow
// policy. A dropped event is not retried by this package; internal/hlsd's
// periodic re-list against GET /channels bounds how stale that can leave a
// subscriber for channel.* events specifically.
func (p *EventPushServer) Publish(evt JournalEvent) {
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
