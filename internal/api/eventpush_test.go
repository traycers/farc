package api

import (
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/toc"
)

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/events/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestEventPushServer_ForwardsMatchingEvent(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1"})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	// Give ServeHTTP time to process the subscribe message and register with
	// NotificationBus before publishing -- otherwise the event might be
	// published before Subscribe runs and get silently dropped (by design,
	// NotificationBus doesn't retain history).
	time.Sleep(50 * time.Millisecond)
	u.Notify().Publish(storage.Event{Name: storage.EventFblockWriteCompleted, Index: 3})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read push: %v", err)
	}
	if msg.Type != "event" || msg.Name != storage.EventFblockWriteCompleted || msg.Index != 3 {
		t.Fatalf("msg = %+v", msg)
	}
}

// TestEventPushServer_PerStorageIncludePool exercises the pool-status-list
// live transport (.scratch/fblocks-ui/issues/04-pool-status-list-plan.md,
// design point 5 / spec.md item 13): a per-storage subscriber that sets
// IncludePool gets a "pool" frame with one row per PoolTuning.Size slot,
// without waiting for any storage.Event.
func TestEventPushServer_PerStorageIncludePool(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1", IncludePool: true})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg poolPushMessage
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read pool push: %v", err)
	}
	if msg.Type != "pool" {
		t.Fatalf("msg.Type = %q, want %q", msg.Type, "pool")
	}
	if msg.Storage != "s1" {
		t.Fatalf("msg.Storage = %q, want %q", msg.Storage, "s1")
	}
	if len(msg.Slots) == 0 {
		t.Fatal("msg.Slots is empty, want one row per PoolTuning.Size slot")
	}
	for i, sl := range msg.Slots {
		if sl.State != "free" {
			t.Fatalf("slot %d state = %q, want %q (nothing reserved yet)", i, sl.State, "free")
		}
	}
}

// TestEventPushServer_PerStorageWithoutIncludePool verifies IncludePool's
// default-false behavior: no "pool" frame (nor the ticker that would
// produce one) is ever sent to a subscriber that didn't opt in.
func TestEventPushServer_PerStorageWithoutIncludePool(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1"})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var extra poolPushMessage
	err = conn.ReadJSON(&extra)
	if err == nil {
		t.Fatalf("unexpected pool frame without IncludePool: %+v", extra)
	}
}

func TestEventPushServer_FiltersByWant(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1", Want: []string{storage.EventStorageAlert}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	u.Notify().Publish(storage.Event{Name: storage.EventFblockWriteCompleted, Index: 1}) // filtered out
	u.Notify().Publish(storage.Event{Name: storage.EventStorageAlert, Severity: "critical", Reason: storage.AlertNoFreeFblocks})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read push: %v", err)
	}
	if msg.Name != storage.EventStorageAlert || msg.Reason != storage.AlertNoFreeFblocks {
		t.Fatalf("msg = %+v, want only the alert to survive the want filter", msg)
	}
}

// TestEventPushServer_FiltersByChannel exercises the real channel_bitmap
// path (matchesSubscription), not just the want-name filter: a fblock event
// for an index whose catalog entry actually has channel 1's bit set must
// reach a subscriber filtered to channel 1, and must not reach one filtered
// to channel 2 only.
func TestEventPushServer_FiltersByChannel(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatalf("ResolveUUID: not found")
	}
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	connMatch := dialWS(t, srv)
	err = connMatch.WriteJSON(subscribeMessage{Storage: "s1", Channels: []uint16{1}})
	if err != nil {
		t.Fatalf("write subscribe (match): %v", err)
	}
	connNoMatch := dialWS(t, srv)
	err = connNoMatch.WriteJSON(subscribeMessage{Storage: "s1", Channels: []uint16{2}})
	if err != nil {
		t.Fatalf("write subscribe (no match): %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	u.Notify().Publish(storage.Event{Name: storage.EventFblockWriteCompleted, Index: idx})

	connMatch.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = connMatch.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("channel-1 subscriber: read push: %v", err)
	}
	if msg.Index != idx {
		t.Fatalf("msg = %+v, want index %d", msg, idx)
	}

	connNoMatch.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if err := connNoMatch.ReadJSON(&msg); err == nil {
		t.Fatalf("channel-2 subscriber unexpectedly received %+v", msg)
	}
}

func TestEventPushServer_UnknownStorage(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: "nope"})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]string
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read error message: %v", err)
	}
	if msg["error"] == "" {
		t.Fatalf("msg = %+v, want an error field", msg)
	}
}

// TestEventPushServer_ServesGlobalSubscription confirms Storage == "" is
// treated as a global (channel-lifecycle-only) subscription, not an
// "unknown storage" error the way any other non-empty, unregistered id is
// (TestEventPushServer_UnknownStorage).
func TestEventPushServer_ServesGlobalSubscription(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: ""})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.Publish(JournalEvent{Name: EventChannelCreated, Channel: 7, Storage: "disk0"})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read push: %v", err)
	}
	if msg.Type != "event" || msg.Name != EventChannelCreated || msg.Channel != 7 || msg.Storage != "disk0" {
		t.Fatalf("msg = %+v", msg)
	}
}

// TestEventPushServer_GlobalFiltersByWant mirrors
// TestEventPushServer_FiltersByWant for the global subscription path.
func TestEventPushServer_GlobalFiltersByWant(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: "", Want: []string{EventChannelRemoved}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.Publish(JournalEvent{Name: EventChannelCreated, Channel: 1, Storage: "disk0"}) // filtered out
	push.Publish(JournalEvent{Name: EventChannelRemoved, Channel: 2, Storage: "disk0"})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = conn.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("read push: %v", err)
	}
	if msg.Name != EventChannelRemoved || msg.Channel != 2 {
		t.Fatalf("msg = %+v, want only the removed event to survive the want filter", msg)
	}
}

// TestEventPushServer_GlobalDropsWhenSubscriberBufferFull confirms
// PublishChannelEvent never blocks the caller (a full subscriber buffer is
// dropped, not queued). It deliberately doesn't try to keep reading from
// the same connection afterward: gorilla/websocket connections aren't
// guaranteed usable after a read has timed out, and "does one specific
// slow reader eventually catch up" isn't a property this policy promises
// anyway -- what matters is that publishing never blocks, and that the
// server as a whole keeps working afterward (checked via a fresh
// connection).
func TestEventPushServer_GlobalDropsWhenSubscriberBufferFull(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: ""})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Flood well past the 64-buffer without reading anything -- must not
	// block regardless of how many of these get dropped.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			push.Publish(JournalEvent{Name: EventChannelCreated, Channel: uint16(i % 65536), Storage: "disk0"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("PublishChannelEvent blocked with a full subscriber buffer")
	}
	conn.Close()

	// The server as a whole must still work: a fresh connection sees a
	// fresh publish.
	conn2 := dialWS(t, srv)
	err = conn2.WriteJSON(subscribeMessage{Storage: ""})
	if err != nil {
		t.Fatalf("write subscribe (second conn): %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	push.Publish(JournalEvent{Name: EventChannelRemoved, Channel: 999, Storage: "disk0"})
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	err = conn2.ReadJSON(&msg)
	if err != nil {
		t.Fatalf("server unusable after overflowing an earlier subscriber: %v", err)
	}
	if msg.Channel != 999 {
		t.Fatalf("msg = %+v, want channel 999", msg)
	}
}

// TestEventPushServer_ConcurrentPublishAndConnect drives concurrent
// PublishChannelEvent calls against concurrently connecting/disconnecting
// global subscribers -- meant to be run with -race.
func TestEventPushServer_ConcurrentPublishAndConnect(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				push.Publish(JournalEvent{Name: EventChannelCreated, Channel: 1, Storage: "disk0"})
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				conn := dialWS(t, srv)
				_ = conn.WriteJSON(subscribeMessage{Storage: ""})
				conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
				var msg pushMessage
				_ = conn.ReadJSON(&msg)
				conn.Close()
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestEventPushServer_GlobalIncludeTOC exercises A2's TOC-over-WS push: a
// global subscriber that sets IncludeTOC gets a "toc" frame right after an
// EventFblockReady "event" frame, carrying the real TOC bytes for the fblock
// that was actually written (not synthetic data) -- lets a subscriber act
// on a fblock's TOC without polling GET .../toc.
func TestEventPushServer_GlobalIncludeTOC(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "", IncludeTOC: true})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "framedata", 150, 1000)
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatal("ResolveUUID: fblock just written not found")
	}
	push.Publish(JournalEvent{
		Name: EventFblockReady, Storage: "s1", Index: idx, UUID: hex.EncodeToString(uuid[:]),
		Begin: 100, End: 200,
	})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var evMsg pushMessage
	err = conn.ReadJSON(&evMsg)
	if err != nil {
		t.Fatalf("read event frame: %v", err)
	}
	if evMsg.Type != "event" || evMsg.Name != EventFblockReady || evMsg.Storage != "s1" || evMsg.Begin != 100 || evMsg.End != 200 {
		t.Fatalf("event frame = %+v", evMsg)
	}

	var tocMsg tocPushMessage
	err = conn.ReadJSON(&tocMsg)
	if err != nil {
		t.Fatalf("read toc frame: %v", err)
	}
	if tocMsg.Type != "toc" || tocMsg.Storage != "s1" || tocMsg.Index != idx || tocMsg.UUID != evMsg.UUID {
		t.Fatalf("toc frame = %+v", tocMsg)
	}
	if len(tocMsg.TOC) == 0 {
		t.Fatal("toc frame carries no bytes")
	}
	columns, err := toc.Decode(tocMsg.TOC)
	if err != nil {
		t.Fatalf("decode pushed toc: %v", err)
	}
	if columns.N == 0 {
		t.Fatal("decoded toc has no rows")
	}
}

// TestEventPushServer_PerStorageIncludeTOC mirrors
// TestEventPushServer_GlobalIncludeTOC for a per-storage subscription
// (Storage: "s1") -- hls_server's EventSubscriber uses exactly this
// subscription shape (channel-filtered, not global), and today it gets no
// "toc" frame at all regardless of IncludeTOC (issue
// .scratch/hls-toc-bootstrap/issues/01-toc-via-ws-push.md's scope
// correction: IncludeTOC was only ever wired into serveGlobal).
func TestEventPushServer_PerStorageIncludeTOC(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1", IncludeTOC: true, Want: []string{storage.EventFblockWriteCompleted}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// A real write, not a synthetic Notify().Publish -- WriteFcontainer
	// itself fires the real fblock.write.completed event via u.Notify(),
	// which the per-storage loop (already subscribed above) picks up live.
	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "framedata", 150, 1000)
	idx, ok := u.ResolveUUID(uuid)
	if !ok {
		t.Fatal("ResolveUUID: fblock just written not found")
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var evMsg pushMessage
	err = conn.ReadJSON(&evMsg)
	if err != nil {
		t.Fatalf("read event frame: %v", err)
	}
	if evMsg.Type != "event" || evMsg.Name != storage.EventFblockWriteCompleted || evMsg.Index != idx {
		t.Fatalf("event frame = %+v", evMsg)
	}

	var tocMsg tocPushMessage
	err = conn.ReadJSON(&tocMsg)
	if err != nil {
		t.Fatalf("read toc frame: %v", err)
	}
	if tocMsg.Type != "toc" || tocMsg.Storage != "s1" || tocMsg.Index != idx || tocMsg.UUID != hex.EncodeToString(uuid[:]) {
		t.Fatalf("toc frame = %+v", tocMsg)
	}
	if len(tocMsg.TOC) == 0 {
		t.Fatal("toc frame carries no bytes")
	}
	columns, err := toc.Decode(tocMsg.TOC)
	if err != nil {
		t.Fatalf("decode pushed toc: %v", err)
	}
	if columns.N == 0 {
		t.Fatal("decoded toc has no rows")
	}
}

// TestEventPushServer_PerStorageWithoutIncludeTOC mirrors
// TestEventPushServer_GlobalWithoutIncludeTOC for a per-storage
// subscription: the existing default-false behavior
// (TestEventPushServer_ForwardsMatchingEvent) must keep holding once the
// per-storage loop starts checking sub.IncludeTOC at all.
func TestEventPushServer_PerStorageWithoutIncludeTOC(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: "s1", Want: []string{storage.EventFblockWriteCompleted}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	_ = writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "framedata", 150, 1000)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var evMsg pushMessage
	if err := conn.ReadJSON(&evMsg); err != nil {
		t.Fatalf("read event frame: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var extra pushMessage
	err = conn.ReadJSON(&extra)
	if err == nil {
		t.Fatalf("unexpected second frame without IncludeTOC: %+v", extra)
	}
}

// TestEventPushServer_GlobalWithoutIncludeTOC verifies IncludeTOC's default
// (false) still suppresses the "toc" frame -- ordinary Journal/UI clients
// must never pay for a payload they didn't ask for.
func TestEventPushServer_GlobalWithoutIncludeTOC(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("s1", u, "s1.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err = conn.WriteJSON(subscribeMessage{Storage: ""})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	uuid := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "framedata", 150, 1000)
	idx, _ := u.ResolveUUID(uuid)
	push.Publish(JournalEvent{Name: EventFblockReady, Storage: "s1", Index: idx, UUID: hex.EncodeToString(uuid[:])})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var evMsg pushMessage
	if err := conn.ReadJSON(&evMsg); err != nil {
		t.Fatalf("read event frame: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var extra pushMessage
	err = conn.ReadJSON(&extra)
	if err == nil {
		t.Fatalf("unexpected second frame without IncludeTOC: %+v", extra)
	}
}
