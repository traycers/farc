package api

import (
	"net/http/httptest"
	"strings"
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
	if err := reg.Register("s1", u, "s1.img"); err != nil {
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
	if err := reg.Register("s1", u, "s1.img"); err != nil {
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
	if err := reg.Register("s1", u, "s1.img"); err != nil {
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
