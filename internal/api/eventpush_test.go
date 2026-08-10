package api

import (
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
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
// that was actually written (not synthetic data) -- the mechanism msm_server
// relies on to compute vaa-blocks without polling GET .../toc.
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

func TestLiveNodeFromElement(t *testing.T) {
	scalar := mediatree.Element{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Parent: 1, Value: []byte{1, 0, 0, 0}}
	n := liveNodeFromElement(5, scalar)
	if n.ID != 5 || n.Role != "channel" || n.Type != "uint32" || n.Parent != 1 {
		t.Fatalf("liveNodeFromElement(scalar) = %+v", n)
	}
	if v, ok := n.Value.(uint32); !ok || v != 1 {
		t.Fatalf("Value = %#v, want uint32(1)", n.Value)
	}
	if n.Size != 0 {
		t.Fatalf("Size = %d, want 0 for a scalar node", n.Size)
	}

	bytesNode := mediatree.Element{Type: mediatree.TypeBytes, Role: mediatree.RoleFrameDataVideo, Parent: 3, Value: []byte("hello")}
	n2 := liveNodeFromElement(6, bytesNode)
	if n2.Value != nil {
		t.Fatalf("Value = %#v, want nil for a bytes node", n2.Value)
	}
	if n2.Size != 5 {
		t.Fatalf("Size = %d, want 5", n2.Size)
	}

	voidNode := mediatree.Element{Type: mediatree.TypeVoid, Role: mediatree.RoleChannels, Parent: 0}
	n3 := liveNodeFromElement(1, voidNode)
	if n3.Value != nil || n3.Size != 0 {
		t.Fatalf("liveNodeFromElement(void) = %+v, want no value/size", n3)
	}
}

// TestEventPushServer_GlobalPublishLive_DeliveredWhenChannelSubscribed
// exercises the fblock-live page's other WS mechanism: a global subscriber
// that lists channel 1 in subscribeMessage.Channels receives a "live" frame
// PublishLive sends for that channel.
func TestEventPushServer_GlobalPublishLive_DeliveredWhenChannelSubscribed(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: "", Channels: []uint16{1}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.PublishLive(livePushMessage{
		Type: "live", Storage: "s1", Channel: 1, Total: 3,
		Nodes: []liveNode{{ID: 0, Role: "root", Type: "void"}},
	})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg livePushMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read live frame: %v", err)
	}
	if msg.Type != "live" || msg.Storage != "s1" || msg.Channel != 1 || msg.Total != 3 || len(msg.Nodes) != 1 {
		t.Fatalf("live frame = %+v", msg)
	}
}

// TestEventPushServer_PublishLiveProgress_ComputesContentBytesAndTocEstimate
// verifies PublishLiveProgress forwards contentBytes verbatim and computes
// EstimatedTocBytes from total via the real toc.ComputeOffsets formula (not
// a hand-rolled approximation) -- the two numbers the fblock-live page's
// fill bar reads.
func TestEventPushServer_PublishLiveProgress_ComputesContentBytesAndTocEstimate(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: "", Channels: []uint16{1}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	elems := []mediatree.Element{
		{Type: mediatree.TypeVoid, Role: mediatree.RoleRoot},
		{Type: mediatree.TypeUint32, Role: mediatree.RoleChannel, Value: []byte{1, 0, 0, 0}},
	}
	const total = 5 // pretend 3 earlier elements were already delivered
	const contentBytes = 12345
	push.PublishLiveProgress("s1", 1, total, contentBytes, elems, 3)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg livePushMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read live frame: %v", err)
	}
	if msg.ContentBytes != contentBytes {
		t.Fatalf("ContentBytes = %d, want %d", msg.ContentBytes, contentBytes)
	}
	want := uint64(toc.ComputeOffsets(total).Total)
	if msg.EstimatedTocBytes != want {
		t.Fatalf("EstimatedTocBytes = %d, want %d (toc.ComputeOffsets(%d).Total)", msg.EstimatedTocBytes, want, total)
	}
	if len(msg.Nodes) != len(elems) || msg.Nodes[0].ID != 3 || msg.Nodes[1].ID != 4 {
		t.Fatalf("Nodes = %+v, want ids starting at firstID=3", msg.Nodes)
	}
}

// TestEventPushServer_GlobalPublishLive_FiltersOtherChannels verifies a
// subscriber only sees live progress for channels it actually listed.
func TestEventPushServer_GlobalPublishLive_FiltersOtherChannels(t *testing.T) {
	reg := NewStorageRegistry()
	push := NewEventPushServer(reg)
	s := NewHttpApiServer(reg, nil, push)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	conn := dialWS(t, srv)
	err := conn.WriteJSON(subscribeMessage{Storage: "", Channels: []uint16{1}})
	if err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	push.PublishLive(livePushMessage{Type: "live", Channel: 2, Total: 1})

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var msg livePushMessage
	err = conn.ReadJSON(&msg)
	if err == nil {
		t.Fatalf("unexpected live frame for unsubscribed channel: %+v", msg)
	}
}

// TestEventPushServer_GlobalPublishLive_RequiresChannelsSubscription
// verifies that omitting Channels entirely (the ordinary Journal/UI
// subscription today) suppresses live progress altogether, not just for
// unlisted channels -- see livePushMessage's own doc comment: unscoped
// delivery would be meaningless.
func TestEventPushServer_GlobalPublishLive_RequiresChannelsSubscription(t *testing.T) {
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

	push.PublishLive(livePushMessage{Type: "live", Channel: 1, Total: 1})

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var msg livePushMessage
	err = conn.ReadJSON(&msg)
	if err == nil {
		t.Fatalf("unexpected live frame without a Channels subscription: %+v", msg)
	}
}
