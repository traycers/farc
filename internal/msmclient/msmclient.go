// Package msmclient is msm_server's read side: a WS client for farcd's
// global EventPushServer feed (internal/api/eventpush.go), subscribed with
// IncludeTOC so it also receives the "toc" frame that follows every
// fblock.ready event. Every event msm_server needs (channel/recording
// lifecycle, fblock lifecycle) is already bridged into that one global
// feed by internal/farcd -- unlike hls_server's internal/hlsclient, this
// package never needs a per-storage subscription or any of farcd's HTTP
// read API (candidates/resolve/toc-over-HTTP), so it stays a single small
// type rather than importing/forking hlsclient.
package msmclient

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/gorilla/websocket"
)

// Event is a decoded WS push frame -- a typed mirror of internal/api's
// pushMessage/tocPushMessage (both shapes fold into one Go struct, since
// their JSON fields never collide and Type tells them apart). Which fields
// are meaningful depends on Type/Name -- see internal/api/eventpush.go's
// JournalEvent doc comment, which this mirrors field-for-field.
type Event struct {
	Type     string // "event" or "toc"
	Name     string
	Channel  uint16
	Storage  string
	Index    uint32
	UUID     [16]byte
	HasUUID  bool
	Severity string
	Reason   string
	Begin    uint64
	End      uint64
	TOC      []byte // only set when Type == "toc"
}

// wireSubscribeMessage mirrors internal/api's subscribeMessage -- only the
// fields this package ever sends (Storage always "", IncludeTOC always
// true: see this package's doc comment on why a global-only, TOC-included
// subscription covers everything msm_server needs).
type wireSubscribeMessage struct {
	Storage    string `json:"storage"`
	IncludeTOC bool   `json:"include_toc"`
}

// wireMessage mirrors the union of internal/api's pushMessage and
// tocPushMessage -- decoding both shapes into one struct is safe since
// their JSON field sets don't overlap except Type/Storage/Index/UUID, which
// mean the same thing in both.
type wireMessage struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Index    uint32 `json:"index,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Channel  uint16 `json:"channel,omitempty"`
	Storage  string `json:"storage,omitempty"`
	Begin    uint64 `json:"begin,omitempty"`
	End      uint64 `json:"end,omitempty"`
	TOC      []byte `json:"toc,omitempty"`
}

// Client dials one farcd's EventPushServer.
type Client struct {
	wsBase string
}

// New creates a Client for wsBase, a base URL with no trailing slash, e.g.
// "ws://127.0.0.1:8081".
func New(wsBase string) *Client {
	return &Client{wsBase: wsBase}
}

// Subscribe dials farcd's EventPushServer (GET /events/ws), sends the one
// global/IncludeTOC subscribe message, and streams decoded events until ctx
// is cancelled or the connection ends. The returned channel closes when the
// connection ends for any reason -- this package does not retry or catch up
// on missed events itself (farcd's WS feed is best-effort with no replay,
// internal/api/eventpush.go's own doc comment); internal/msmd owns
// reconnect-on-disconnect.
func (c *Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsBase+"/events/ws", nil) //nolint:bodyclose // gorilla/websocket's own doc comment: the handshake response body needs no closing
	if err != nil {
		return nil, fmt.Errorf("msmclient: subscribe: dial: %w", err)
	}

	sub := wireSubscribeMessage{Storage: "", IncludeTOC: true}
	err = conn.WriteJSON(sub)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("msmclient: subscribe: send subscribe message: %w", err)
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
			var msg wireMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				return
			}
			ev := Event{
				Type: msg.Type, Name: msg.Name, Channel: msg.Channel, Storage: msg.Storage,
				Index: msg.Index, Severity: msg.Severity, Reason: msg.Reason,
				Begin: msg.Begin, End: msg.End, TOC: msg.TOC,
			}
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

func decodeHexUUID(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("msmclient: invalid uuid %q: %w", s, err)
	}
	if len(b) != 16 {
		return out, fmt.Errorf("msmclient: invalid uuid %q: want 16 bytes, got %d", s, len(b))
	}
	copy(out[:], b)
	return out, nil
}
