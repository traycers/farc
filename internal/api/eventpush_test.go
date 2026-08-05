package api

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
)

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/events/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestEventPushServer_ForwardsMatchingEvent(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	if err := conn.WriteJSON(subscribeMessage{Storage: "s1"}); err != nil {
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
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read push: %v", err)
	}
	if msg.Type != "event" || msg.Name != storage.EventFblockWriteCompleted || msg.Index != 3 {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestEventPushServer_FiltersByWant(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	if err := conn.WriteJSON(subscribeMessage{Storage: "s1", Want: []string{storage.EventStorageAlert}}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	u.Notify().Publish(storage.Event{Name: storage.EventFblockWriteCompleted, Index: 1}) // filtered out
	u.Notify().Publish(storage.Event{Name: storage.EventStorageAlert, Severity: "critical", Reason: storage.AlertNoFreeFblocks})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	if err := conn.ReadJSON(&msg); err != nil {
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
	if err := reg.Register("s1", u, "s1.img", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	connMatch := dialWS(t, srv)
	if err := connMatch.WriteJSON(subscribeMessage{Storage: "s1", Channels: []uint16{1}}); err != nil {
		t.Fatalf("write subscribe (match): %v", err)
	}
	connNoMatch := dialWS(t, srv)
	if err := connNoMatch.WriteJSON(subscribeMessage{Storage: "s1", Channels: []uint16{2}}); err != nil {
		t.Fatalf("write subscribe (no match): %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	u.Notify().Publish(storage.Event{Name: storage.EventFblockWriteCompleted, Index: idx})

	connMatch.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	if err := connMatch.ReadJSON(&msg); err != nil {
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
	if err := conn.WriteJSON(subscribeMessage{Storage: "nope"}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]string
	if err := conn.ReadJSON(&msg); err != nil {
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
	if err := conn.WriteJSON(subscribeMessage{Storage: ""}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.PublishChannelEvent(ChannelEvent{Name: EventChannelCreated, Channel: 7, Storage: "disk0"})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	if err := conn.ReadJSON(&msg); err != nil {
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
	if err := conn.WriteJSON(subscribeMessage{Storage: "", Want: []string{EventChannelRemoved}}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.PublishChannelEvent(ChannelEvent{Name: EventChannelCreated, Channel: 1, Storage: "disk0"}) // filtered out
	push.PublishChannelEvent(ChannelEvent{Name: EventChannelRemoved, Channel: 2, Storage: "disk0"})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	if err := conn.ReadJSON(&msg); err != nil {
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
	if err := conn.WriteJSON(subscribeMessage{Storage: ""}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Flood well past the 64-buffer without reading anything -- must not
	// block regardless of how many of these get dropped.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			push.PublishChannelEvent(ChannelEvent{Name: EventChannelCreated, Channel: uint16(i % 65536), Storage: "disk0"})
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
	if err := conn2.WriteJSON(subscribeMessage{Storage: ""}); err != nil {
		t.Fatalf("write subscribe (second conn): %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	push.PublishChannelEvent(ChannelEvent{Name: EventChannelRemoved, Channel: 999, Storage: "disk0"})
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg pushMessage
	if err := conn2.ReadJSON(&msg); err != nil {
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
				push.PublishChannelEvent(ChannelEvent{Name: EventChannelCreated, Channel: 1, Storage: "disk0"})
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
