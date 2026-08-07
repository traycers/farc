// Package msmapi is a typed outbound HTTP client for the external msm
// ("media services manager") service's controller-tagged operations
// (temp/msm/openapi.yaml) -- the 8 calls internal/msmd makes in response to
// farcd WS events: ParamsAdd, FblocksAdd, FblocksDel, StatusSet, InfoSet,
// StartedAdd, FinishedAdd, VaaBlocksAdd. msm's error body shape
// ({"code","message"}, models.errors.Error) differs from farc's own HTTP
// API's {"error":"..."}, so this package decodes msm's shape specifically
// rather than reusing anything from internal/api/internal/hlsclient.
package msmapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an msm API client. base has no trailing slash.
type Client struct {
	base string
	hc   *http.Client
}

// New creates a Client for base, msm's base URL (e.g. "http://msm:9000") --
// same bare *http.Client{} (no explicit timeout, no retry) as
// internal/hlsclient.Client, matching this codebase's existing outbound-HTTP
// convention.
func New(base string) *Client {
	return &Client{base: strings.TrimSuffix(base, "/"), hc: &http.Client{}}
}

// formatUUID renders b as a dashed lowercase UUID string (8-4-4-4-12),
// matching temp/msm/openapi.yaml's `format: uuid` fields -- farc's own HTTP
// API uses undashed hex for the same 16 bytes internally (internal/api's
// parseUUID), but that's a farc-only convention msm doesn't share.
func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// errorBody mirrors models.errors.Error ({"code","message"}).
type errorBody struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// do issues an HTTP request with an optional JSON body, translating msm's
// {"code","message"} error bodies into a descriptive Go error on a non-2xx
// status. None of this package's 8 operations return a response body on
// success (temp/msm/openapi.yaml: every one of them is a bare 200), so
// unlike internal/hlsclient's do this doesn't return the body at all.
func (c *Client) do(ctx context.Context, method, path string, body any) error {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("msmapi: marshal request for %s %s: %w", method, path, err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return fmt.Errorf("msmapi: build request for %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("msmapi: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("msmapi: %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		var e errorBody
		if json.Unmarshal(respBody, &e) == nil && e.Message != "" {
			return fmt.Errorf("msmapi: %s %s: %s (code %d, status %d)", method, path, e.Message, e.Code, resp.StatusCode)
		}
		return fmt.Errorf("msmapi: %s %s: status %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParamsAdd implements POST /api/v1/archives/{aid}/streams/params/
// (params_add): register a stream's codec parameters under id, minted and
// owned by internal/msmd (msm has no id-assignment endpoint of its own).
// data is marshaled as-is -- internal/vaablocks builds the video/audio
// schema shape described in the params-format docs the caller was given;
// this package doesn't need to know it.
func (c *Client) ParamsAdd(ctx context.Context, aid string, id int64, streamType int, data any) error {
	body := struct {
		ID   int64 `json:"id"`
		Type int   `json:"type"`
		Data any   `json:"data"`
	}{ID: id, Type: streamType, Data: data}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/archives/%s/streams/params/", aid), body)
}

// FblocksAdd implements POST /api/v1/archives/{aid}/fblocks/ (fblocks_add).
func (c *Client) FblocksAdd(ctx context.Context, aid string, id [16]byte, num int64) error {
	body := struct {
		ID  string `json:"id"`
		Num int64  `json:"num"`
	}{ID: formatUUID(id), Num: num}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/archives/%s/fblocks/", aid), body)
}

// FblocksDel implements DELETE /api/v1/archives/{aid}/fblocks/?id=
// (fblocks_del).
func (c *Client) FblocksDel(ctx context.Context, aid string, id [16]byte) error {
	path := fmt.Sprintf("/api/v1/archives/%s/fblocks/?id=%s", aid, formatUUID(id))
	return c.do(ctx, http.MethodDelete, path, nil)
}

// StatusSet implements PUT /api/v1/archives/{aid}/fblocks/{fbid}/status/
// (status_set).
func (c *Client) StatusSet(ctx context.Context, aid string, fbid [16]byte, status int) error {
	body := struct {
		Status int `json:"status"`
	}{Status: status}
	path := fmt.Sprintf("/api/v1/archives/%s/fblocks/%s/status/", aid, formatUUID(fbid))
	return c.do(ctx, http.MethodPut, path, body)
}

// InfoSet implements PUT /api/v1/archives/{aid}/fblocks/{fbid}/info/
// (info_set) -- internal/msmd calls this only after every VaaBlocksAdd for
// the same fbid has completed (temp/msm/openapi.yaml's documented ordering
// requirement), which this package has no part in enforcing itself.
func (c *Client) InfoSet(ctx context.Context, aid string, fbid [16]byte, num int64, status int, start, stop time.Time) error {
	body := struct {
		Num    int64  `json:"num"`
		Status int    `json:"status"`
		Start  string `json:"start"`
		Stop   string `json:"stop"`
	}{Num: num, Status: status, Start: formatTime(start), Stop: formatTime(stop)}
	path := fmt.Sprintf("/api/v1/archives/%s/fblocks/%s/info/", aid, formatUUID(fbid))
	return c.do(ctx, http.MethodPut, path, body)
}

// StartedAdd implements
// POST /api/v1/archives/{aid}/channels/{cid}/recording/started/
// (started_add).
func (c *Client) StartedAdd(ctx context.Context, aid string, channel uint16, begin time.Time) error {
	body := struct {
		Begin string `json:"begin"`
	}{Begin: formatTime(begin)}
	path := fmt.Sprintf("/api/v1/archives/%s/channels/%d/recording/started/", aid, channel)
	return c.do(ctx, http.MethodPost, path, body)
}

// FinishedAdd implements
// POST /api/v1/archives/{aid}/channels/{cid}/recording/finished/
// (finished_add).
func (c *Client) FinishedAdd(ctx context.Context, aid string, channel uint16, end time.Time) error {
	body := struct {
		End string `json:"end"`
	}{End: formatTime(end)}
	path := fmt.Sprintf("/api/v1/archives/%s/channels/%d/recording/finished/", aid, channel)
	return c.do(ctx, http.MethodPost, path, body)
}

// VaaBlockID is models.vaa_block.Id: a vaa-block's composite identifier
// (which fblock, and its byte range within that fblock's Content section).
type VaaBlockID struct {
	Fnum   int64
	Offset int32
	Size   int32
}

// VaaBlock is models.vaa_block.Info minus archive_id, which VaaBlocksAdd
// takes as its own aid parameter (like every other method here) and fills
// into both the URL and the body's archive_id field -- the spec asks for
// archive_id in the body too, but it can never legitimately differ from the
// path's {aid}, so callers only have to supply it once.
type VaaBlock struct {
	ID         VaaBlockID
	FblockID   [16]byte
	ChannelNum int32
	ParamsID   int64
	StreamID   int16
	StreamType int
	Begin      time.Time
	End        time.Time
}

// VaaBlocksAdd implements POST /api/v1/archives/{aid}/vaa_blocks/
// (vaa_blocks_add).
func (c *Client) VaaBlocksAdd(ctx context.Context, aid string, block VaaBlock) error {
	body := struct {
		ID struct {
			Fnum   int64 `json:"fnum"`
			Offset int32 `json:"offset"`
			Size   int32 `json:"size"`
		} `json:"id"`
		FblockID   string `json:"fblock_id"`
		ArchiveID  string `json:"archive_id"`
		ChannelNum int32  `json:"channel_num"`
		ParamsID   int64  `json:"params_id"`
		StreamID   int16  `json:"stream_id"`
		StreamType int    `json:"stream_type"`
		Begin      string `json:"begin"`
		End        string `json:"end"`
	}{
		FblockID: formatUUID(block.FblockID), ArchiveID: aid, ChannelNum: block.ChannelNum,
		ParamsID: block.ParamsID, StreamID: block.StreamID, StreamType: block.StreamType,
		Begin: formatTime(block.Begin), End: formatTime(block.End),
	}
	body.ID.Fnum, body.ID.Offset, body.ID.Size = block.ID.Fnum, block.ID.Offset, block.ID.Size
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/archives/%s/vaa_blocks/", aid), body)
}
