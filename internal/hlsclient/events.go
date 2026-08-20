package hlsclient

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
)

// EventChannelCreated/EventChannelRemoved mirror internal/api's
// EventChannelCreated/EventChannelRemoved -- the Name values a "global"
// subscription (storageID == "" in Subscribe below) delivers, duplicated
// here rather than imported since internal/api's constants aren't meant to
// be depended on directly (see this package's own doc comment).
const (
	EventChannelCreated = "channel.created"
	EventChannelRemoved = "channel.removed"
)

// Event is a decoded WS push frame — a typed mirror of internal/api's
// pushMessage/tocPushMessage (both shapes fold into one struct, since their
// JSON fields never collide), with UUID decoded from hex into [16]byte.
// Channel/Storage are only set for a
// global subscription's channel-lifecycle events (Name ==
// EventChannelCreated/EventChannelRemoved); Index/UUID/Severity/Reason are
// only set for a per-storage fblock event; TOC is only set when
// Type == "toc" (a Subscribe call made with includeTOC=true).
type Event struct {
	Type     string
	Name     string // storage.Event* name, e.g. "fblock.write.completed", or EventChannelCreated/Removed
	Index    uint32
	UUID     [16]byte
	HasUUID  bool
	Severity string
	Reason   string
	Channel  uint16
	Storage  string
	TOC      []byte
}

// wireSubscribeMessage mirrors internal/api's subscribeMessage.
type wireSubscribeMessage struct {
	Storage    string   `json:"storage"`
	Want       []string `json:"want"`
	Channels   []uint16 `json:"channels"`
	IncludeTOC bool     `json:"include_toc"`
}

// wirePushMessage mirrors the union of internal/api's pushMessage and
// tocPushMessage -- decoding both shapes into one struct is safe since their
// JSON field sets don't overlap except Type/Storage/Index/UUID, which mean
// the same thing in both.
type wirePushMessage struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Channel  uint16 `json:"channel,omitempty"`
	Storage  string `json:"storage,omitempty"`
	TOC      []byte `json:"toc,omitempty"`
}

// Subscribe dials farcd's EventPushServer (GET /events/ws), sends the one
// subscribe message the server expects right after connecting, and streams
// decoded events until ctx is cancelled or the connection ends. want and
// channels behave exactly as internal/api/eventpush.go's
// matchesSubscription — empty means no filter on that dimension.
// storageID == "" is a "global" subscription (ADR-021): no per-storage
// fblock filtering, just channel-lifecycle events (EventChannelCreated/
// EventChannelRemoved) regardless of channels' filtering, which only
// applies to a per-storage subscription. includeTOC requests a follow-up
// "toc" frame (decoded into Event.TOC) right after each
// storage.EventFblockWriteCompleted event -- callers that don't need it
// (internal/hlsd's global channel-lifecycle subscription) should pass
// false, since the server only builds that frame when asked (a fblock
// already recycled past retention by push time yields no "toc" frame at
// all, not an empty one -- callers must not assume it's always present just
// because they asked for it).
//
// The returned channel closes when the connection ends for any reason
// (ctx cancellation, server close, network error); this package does not
// retry or catch up on missed events itself — that is
// internal/tocindex.EventSubscriber's job (ADR-018: reconnect triggers a
// bootstrap resolve, not a resend).
func (c *Client) Subscribe(ctx context.Context, storageID string, want []string, channels []uint16, includeTOC bool) (<-chan Event, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsBase+"/events/ws", nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		return nil, fmt.Errorf("hlsclient: subscribe: dial: %w", err)
	}

	sub := wireSubscribeMessage{Storage: storageID, Want: want, Channels: channels, IncludeTOC: includeTOC}
	err = conn.WriteJSON(sub)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("hlsclient: subscribe: send subscribe message: %w", err)
	}

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		defer func() { _ = conn.Close() }()
		go func() {
			<-ctx.Done()
			_ = conn.Close()
		}()
		for {
			var msg wirePushMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				return
			}
			ev := Event{Type: msg.Type, Name: msg.Name, Index: msg.Index, Severity: msg.Severity, Reason: msg.Reason, Channel: msg.Channel, Storage: msg.Storage, TOC: msg.TOC}
			if msg.UUID != "" {
				uuid, err := decodeHexUUID(msg.UUID)
				if err == nil {
					ev.UUID = uuid
					ev.HasUUID = true
				}
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}
