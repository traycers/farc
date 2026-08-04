package hlsclient

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
)

// Event is a decoded WS push frame — a typed mirror of internal/api's
// pushMessage, with UUID decoded from hex into [16]byte.
type Event struct {
	Type     string
	Name     string // storage.Event* name, e.g. "fblock.write.completed"
	Index    uint32
	UUID     [16]byte
	HasUUID  bool
	Severity string
	Reason   string
}

// wireSubscribeMessage mirrors internal/api's subscribeMessage.
type wireSubscribeMessage struct {
	Storage  string   `json:"storage"`
	Want     []string `json:"want"`
	Channels []uint16 `json:"channels"`
}

// wirePushMessage mirrors internal/api's pushMessage.
type wirePushMessage struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Subscribe dials farcd's EventPushServer (GET /events/ws), sends the one
// subscribe message the server expects right after connecting, and streams
// decoded events until ctx is cancelled or the connection ends. want and
// channels behave exactly as internal/api/eventpush.go's
// matchesSubscription — empty means no filter on that dimension.
//
// The returned channel closes when the connection ends for any reason
// (ctx cancellation, server close, network error); this package does not
// retry or catch up on missed events itself — that is
// internal/tocindex.EventSubscriber's job (ADR-018: reconnect triggers a
// bootstrap resolve, not a resend).
func (c *Client) Subscribe(ctx context.Context, storageID string, want []string, channels []uint16) (<-chan Event, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsBase+"/events/ws", nil)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: subscribe: dial: %w", err)
	}

	sub := wireSubscribeMessage{Storage: storageID, Want: want, Channels: channels}
	if err := conn.WriteJSON(sub); err != nil {
		conn.Close()
		return nil, fmt.Errorf("hlsclient: subscribe: send subscribe message: %w", err)
	}

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		defer conn.Close()
		go func() {
			<-ctx.Done()
			conn.Close()
		}()
		for {
			var msg wirePushMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			ev := Event{Type: msg.Type, Name: msg.Name, Index: msg.Index, Severity: msg.Severity, Reason: msg.Reason}
			if msg.UUID != "" {
				if uuid, err := decodeHexUUID(msg.UUID); err == nil {
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
