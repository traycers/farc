// Package hlsclient is a typed HTTP+WS client for farcd's external read API
// (internal/api's HttpApiServer and EventPushServer) — GetTOC, ReadRanges,
// Candidates, Resolve, Subscribe. hls_server talks to farcd only through
// this client, never touching internal/storage directly
// (docs/docs/archive/12-hls-server.md, PLAN.md's package layout).
//
// internal/api's own wire types (candidateInfo, resolvedFrame,
// subscribeMessage, pushMessage) are unexported, and are in any case an HTTP
// wire contract rather than a Go API to depend on directly — this package
// defines its own matching JSON-tagged types and decodes farcd's hex/base64
// encodings into typed Go values ([16]byte, []byte) for its own callers.
package hlsclient

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"traycers/farc/toc"
)

// Client is a farcd read-API client. farcd runs HTTP API and WS push as two
// separate servers (docs/docs/archive/04-storage-operations.md §2.1), so
// httpBase and wsBase are independent addresses.
type Client struct {
	httpBase string
	wsBase   string
	hc       *http.Client
}

// New creates a Client. httpBase and wsBase are base URLs with no trailing
// slash, e.g. "http://127.0.0.1:8080" and "ws://127.0.0.1:8081".
func New(httpBase, wsBase string) *Client {
	return &Client{
		httpBase: strings.TrimSuffix(httpBase, "/"),
		wsBase:   strings.TrimSuffix(wsBase, "/"),
		hc:       &http.Client{},
	}
}

// Range is one (offset, size) request into a fcontainer's Content section,
// mirroring internal/storage.Range — this package's own copy since
// hlsclient does not depend on internal/storage.
type Range struct {
	Offset uint64
	Size   uint64
}

func decodeBase64(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: invalid base64 data: %w", err)
	}
	return b, nil
}

func decodeHexUUID(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("hlsclient: invalid uuid %q: %w", s, err)
	}
	if len(b) != 16 {
		return out, fmt.Errorf("hlsclient: invalid uuid %q: want 16 bytes, got %d", s, len(b))
	}
	copy(out[:], b)
	return out, nil
}

// do issues a GET request and returns the response body's raw bytes on
// success, translating farcd's {"error": "..."} bodies (internal/api's
// writeError) into a descriptive Go error on non-2xx status.
func (c *Client) do(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.httpBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: build request for %s: %w", path, err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: GET %s: read body: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("hlsclient: GET %s: %s (status %d)", path, e.Error, resp.StatusCode)
		}
		return nil, fmt.Errorf("hlsclient: GET %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	body, err := c.do(ctx, path)
	if err != nil {
		return err
	}
	err = json.Unmarshal(body, out)
	if err != nil {
		return fmt.Errorf("hlsclient: GET %s: decode response: %w", path, err)
	}
	return nil
}

// wireCandidate mirrors internal/api's candidateInfo.
type wireCandidate struct {
	Index uint32 `json:"index"`
	UUID  string `json:"uuid"`
	Begin uint64 `json:"begin"`
	End   uint64 `json:"end"`
}

// Candidate is one GET .../candidates result entry (ADR-014): fblock-level
// candidates only, no TOC confirmation yet.
type Candidate struct {
	Index uint32
	UUID  [16]byte
	Begin uint64
	End   uint64
}

// Candidates implements GET .../candidates?channel=&t1=&t2= (ADR-014).
func (c *Client) Candidates(ctx context.Context, storageID string, channel uint16, t1, t2 uint64) ([]Candidate, error) {
	path := fmt.Sprintf("/storages/%s/candidates?channel=%d&t1=%d&t2=%d", url.PathEscape(storageID), channel, t1, t2)
	var wire []wireCandidate
	err := c.getJSON(ctx, path, &wire)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, len(wire))
	for i, w := range wire {
		uuid, err := decodeHexUUID(w.UUID)
		if err != nil {
			return nil, fmt.Errorf("hlsclient: candidates: %w", err)
		}
		out[i] = Candidate{Index: w.Index, UUID: uuid, Begin: w.Begin, End: w.End}
	}
	return out, nil
}

// wireResolvedFrame mirrors internal/api's resolvedFrame.
type wireResolvedFrame struct {
	UUID string `json:"uuid"`
	Time uint64 `json:"time"`
	Kind *uint8 `json:"kind,omitempty"`
	Data string `json:"data"`
}

// ResolvedFrame is one GET .../resolve result entry — the ADR-016 fallback
// path, used by hls_server only for index bootstrap/reconnect (ADR-018), not
// as the per-request playback path.
type ResolvedFrame struct {
	UUID [16]byte
	Time uint64
	Kind *uint8
	Data []byte
}

// Resolve implements GET .../resolve?channel=&t1=&t2= (ADR-016).
func (c *Client) Resolve(ctx context.Context, storageID string, channel uint16, t1, t2 uint64) ([]ResolvedFrame, error) {
	path := fmt.Sprintf("/storages/%s/resolve?channel=%d&t1=%d&t2=%d", url.PathEscape(storageID), channel, t1, t2)
	var wire []wireResolvedFrame
	err := c.getJSON(ctx, path, &wire)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedFrame, len(wire))
	for i, w := range wire {
		uuid, err := decodeHexUUID(w.UUID)
		if err != nil {
			return nil, fmt.Errorf("hlsclient: resolve: %w", err)
		}
		data, err := decodeBase64(w.Data)
		if err != nil {
			return nil, fmt.Errorf("hlsclient: resolve: %w", err)
		}
		out[i] = ResolvedFrame{UUID: uuid, Time: w.Time, Kind: w.Kind, Data: data}
	}
	return out, nil
}

// GetTOC implements GET .../fcontainers/{uuid}/toc and decodes the result.
func (c *Client) GetTOC(ctx context.Context, storageID string, uuid [16]byte) (*toc.Columns, error) {
	path := fmt.Sprintf("/storages/%s/fcontainers/%s/toc", url.PathEscape(storageID), hex.EncodeToString(uuid[:]))
	buf, err := c.do(ctx, path)
	if err != nil {
		return nil, err
	}
	columns, err := toc.Decode(buf)
	if err != nil {
		return nil, fmt.Errorf("hlsclient: get toc: %w", err)
	}
	return columns, nil
}

// wireChannelInfo mirrors internal/api's channelInfo -- only the fields
// this package's own ChannelInfo actually needs (hls_server's
// reconciliation cares which channel is on which storage, nothing else
// GET /channels reports).
type wireChannelInfo struct {
	Channel uint16 `json:"channel"`
	Storage string `json:"storage"`
}

// ChannelInfo is one GET /channels result entry: a channel number and the
// farcd-side storage id it's currently assigned to.
type ChannelInfo struct {
	Channel uint16
	Storage string
}

// ListChannels implements GET /channels -- every channel currently running
// on farcd, across all its storages. internal/hlsd uses this to reconcile
// its own served-channel set against farcd's live one, at startup and on
// every reconnect to the channel-lifecycle event stream (ADR-021).
func (c *Client) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	var wire []wireChannelInfo
	err := c.getJSON(ctx, "/channels", &wire)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelInfo, len(wire))
	for i, w := range wire {
		out[i] = ChannelInfo(w)
	}
	return out, nil
}

// ReadRanges implements GET .../fcontainers/{uuid}?ranges=off:len,... and
// splits the concatenated response back into per-range byte slices, relying
// on internal/api's handleReadContent writing bufs in the caller's original
// request order.
func (c *Client) ReadRanges(ctx context.Context, storageID string, uuid [16]byte, ranges []Range) ([][]byte, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	parts := make([]string, len(ranges))
	for i, r := range ranges {
		parts[i] = fmt.Sprintf("%d:%d", r.Offset, r.Size)
	}
	path := fmt.Sprintf("/storages/%s/fcontainers/%s?ranges=%s", url.PathEscape(storageID), hex.EncodeToString(uuid[:]), strings.Join(parts, ","))
	buf, err := c.do(ctx, path)
	if err != nil {
		return nil, err
	}

	out := make([][]byte, len(ranges))
	var off uint64
	for i, r := range ranges {
		if off+r.Size > uint64(len(buf)) {
			return nil, fmt.Errorf("hlsclient: read ranges: response too short: need %d bytes, have %d", off+r.Size, len(buf))
		}
		out[i] = buf[off : off+r.Size]
		off += r.Size
	}
	return out, nil
}
