package api

import (
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
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
	Storage  string   `json:"storage"`
	Want     []string `json:"want"`
	Channels []uint16 `json:"channels"`
	// LiveStorages scopes livePushMessage delivery on a global subscription
	// -- required for "live" frames to be delivered at all (an empty set
	// means "no live messages", not "all of them", since live progress is
	// meaningless unscoped to a storage). Unrelated to Channels above, which
	// is the per-storage NotificationBus subscription's own fblock-bitmap
	// filter (ServeHTTP's non-global branch) -- fblock-live's live-progress
	// feed is storage-scoped, not channel-scoped (docs/docs/archive/
	// adr/014-channel-registry.md: one fcontainer commonly holds every
	// channel of a storage at once).
	LiveStorages []string `json:"live_storages"`
	IncludeTOC   bool     `json:"include_toc"`
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

// liveNode is one element of a live progress delta — see livePushMessage.
// Field-compatible with treeNodeJSON's Role/Type/Parent/Value/Size (this
// package's shared decodeScalarValue is reused verbatim, see
// liveNodeFromElement), minus ChildCount, which has no meaning for a flat
// delta list.
type liveNode struct {
	ID     uint32 `json:"id"`
	Role   string `json:"role"`
	Type   string `json:"type"`
	Parent uint32 `json:"parent"`
	Value  any    `json:"value,omitempty"`
	Size   uint64 `json:"size,omitempty"`
}

// liveNodeFromElement decodes a mediatree.Element straight from an
// in-memory fcontainer.Filler (internal/ingest.CapturePolicy.
// LiveElementsSince) — unlike treeNodeJSON's finalized-TOC counterpart,
// Value here is already the node's raw bytes at their exact fixed width
// (Filler.append stores them pre-serialized), so decodeScalarValue applies
// directly with no InlineValue/ContentOffset unpacking step.
func liveNodeFromElement(id uint32, e mediatree.Element) liveNode {
	n := liveNode{ID: id, Role: e.Role.String(), Type: e.Type.String(), Parent: e.Parent}
	if v, ok := decodeScalarValue(e.Type, e.Value); ok {
		n.Value = v
	} else if e.Type.Variable() {
		n.Size = uint64(len(e.Value))
	}
	return n
}

// livePushMessage reports fcontainer tree growth for a storage whose shared
// segment is still being recorded (not yet written to disk — see
// internal/farcd's periodic ticker, the only publisher). One storage's
// fcontainer commonly covers several channels at once (docs/docs/archive/
// adr/014-channel-registry.md), so this is deliberately not scoped to any
// one channel. Nodes is the delta since the subscriber's last-seen Total
// (the fblock-live page's own cursor); Total is the new cursor to pass as
// `since` on the next tick. A client that wants these must also set
// subscribeMessage.LiveStorages, since live progress is meaningless
// unscoped to a storage.
type livePushMessage struct {
	Type              string     `json:"type"` // "live"
	Storage           string     `json:"storage"`
	Total             int        `json:"total"`
	ContentBytes      uint64     `json:"content_bytes"`
	EstimatedTocBytes uint64     `json:"estimated_toc_bytes"`
	Nodes             []liveNode `json:"nodes"`
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
	liveSubs   map[chan livePushMessage]struct{}
}

// NewEventPushServer creates a WS push server over reg. CheckOrigin always
// allows: this is an internal operator/consumer API, not a browser-facing
// public endpoint, so no same-origin policy applies.
func NewEventPushServer(reg *StorageRegistry) *EventPushServer {
	return &EventPushServer{
		reg:        reg,
		upgrader:   websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		globalSubs: make(map[chan JournalEvent]struct{}),
		liveSubs:   make(map[chan livePushMessage]struct{}),
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
	// liveStorages scopes livePushMessage delivery only -- classic events
	// stay unfiltered by storage, matching pre-existing behavior; live
	// progress is meaningless unscoped (see livePushMessage's doc comment),
	// so an empty set here means "no live messages", not "all of them".
	liveStorages := make(map[string]bool, len(sub.LiveStorages))
	for _, s := range sub.LiveStorages {
		liveStorages[s] = true
	}

	events := make(chan JournalEvent, 64)
	live := make(chan livePushMessage, 64)
	p.mu.Lock()
	p.globalSubs[events] = struct{}{}
	p.liveSubs[live] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.globalSubs, events)
		delete(p.liveSubs, live)
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
		case lv := <-live:
			if !liveStorages[lv.Storage] {
				continue
			}
			err := conn.WriteJSON(lv)
			if err != nil {
				return
			}
		}
	}
}

// PublishLiveProgress builds a livePushMessage from elems -- a live
// fcontainer snapshot delta for storageID's shared segment, as returned by
// internal/ingest.IngestManager.LiveElementsSinceStorage -- and fans it out
// via PublishLive. firstID is the absolute (Filler creation-order) id of
// elems[0], i.e. the cursor internal/farcd's periodic ticker passed to
// LiveElementsSinceStorage, so each element's true id can be recovered as
// firstID+i. Keeping liveNode/livePushMessage's wire shape unexported (this
// is the one exported entry point for producing them) means farcd never has
// to import mediatree's role/type stringification or the value/size
// decoding this package already implements for the finalized-tree endpoint.
//
// contentBytes is the segment's total encoded content size so far
// (Filler.ContentBytes, via LiveElementsSinceStorage). EstimatedTocBytes is
// computed here, not passed in, by reusing toc.ComputeOffsets against the
// current node count total -- an exact prediction of the eventual TOC
// section size *if* the segment stopped growing right now (the fblock-live
// page's fill bar treats it as a live estimate, since more nodes may still
// arrive).
func (p *EventPushServer) PublishLiveProgress(storageID string, total int, contentBytes int, elems []mediatree.Element, firstID uint32) {
	nodes := make([]liveNode, len(elems))
	for i, e := range elems {
		nodes[i] = liveNodeFromElement(firstID+uint32(i), e)
	}
	estimatedTocBytes := uint64(toc.ComputeOffsets(uint32(total)).Total)
	p.PublishLive(livePushMessage{
		Type: "live", Storage: storageID, Total: total,
		ContentBytes: uint64(contentBytes), EstimatedTocBytes: estimatedTocBytes, Nodes: nodes,
	})
}

// PublishLive fans msg out to every current global subscriber whose
// subscribeMessage.LiveStorages includes msg.Storage, non-blocking (same
// drop-if-slow policy as Publish) -- internal/farcd's periodic live-progress
// ticker is the only caller.
func (p *EventPushServer) PublishLive(msg livePushMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.liveSubs {
		select {
		case ch <- msg:
		default:
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
