// Package farcctl is an outbound HTTP client for farcd's own generic
// Storage/Channel API (internal/api's /storages and /channels routes) --
// msm_server's archivesapi translates the external controller's
// archive-shaped calls (temp/controller/openapi.yaml) into these, the same
// way farcd's own (now removed) internal/api/archives.go used to compose
// them in-process. Modeled on internal/msmapi.Client's do(ctx, method, path,
// body) helper, generalized to also decode a response body (farcd's generic
// API returns one on success, unlike msm's own bare-200 operations) and to
// decode farcd's {"error":"..."} shape rather than msm's {"code","message"}.
package farcctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/storage"
)

// Client is a farcd generic-API client. base has no trailing slash.
type Client struct {
	base string
	hc   *http.Client
}

// New creates a Client for base, farcd's HTTP API base URL (e.g.
// "http://127.0.0.1:8080") -- same bare *http.Client{} (no explicit timeout,
// no retry) as internal/hlsclient.Client/internal/msmapi.Client, matching
// this codebase's existing outbound-HTTP convention.
func New(base string) *Client {
	return &Client{base: strings.TrimSuffix(base, "/"), hc: &http.Client{}}
}

// APIError is a non-2xx response from farcd, carrying the HTTP status back
// so a caller (archivesapi) can translate it into the controller API's own
// {"code","message"} error shape.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("farcctl: status %d: %s", e.Status, e.Message)
}

// do issues an HTTP request with an optional JSON body, decoding a
// successful response body into out (if out is non-nil and a body was
// returned) or a non-2xx response into an *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("farcctl: marshal request for %s %s: %w", method, path, err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return fmt.Errorf("farcctl: build request for %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("farcctl: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("farcctl: %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		msg := string(respBody)
		if json.Unmarshal(respBody, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(respBody) > 0 {
		err := json.Unmarshal(respBody, out)
		if err != nil {
			return fmt.Errorf("farcctl: %s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}

// CreateStorageRequest is POST /storages' body -- field-for-field the same
// shape as internal/api's createStorageRequest (that package's own doc
// comment: "no separate wire schema").
type CreateStorageRequest struct {
	ID          string           `json:"id"`
	Path        string           `json:"path"`
	Geometry    storage.Geometry `json:"geometry"`
	Params      fblock.Params    `json:"params"`
	Force       bool             `json:"force,omitempty"`
	CatalogPath string           `json:"catalog_path,omitempty"`
	Backend     string           `json:"backend,omitempty"`
	Name        string           `json:"name,omitempty"`
}

// StorageInfo mirrors internal/api's StorageInfo (POST/GET /storages'
// response shape).
type StorageInfo struct {
	ID       string           `json:"id"`
	Path     string           `json:"path"`
	Name     string           `json:"name,omitempty"`
	Geometry storage.Geometry `json:"geometry"`
}

// CreateStorage implements POST /storages.
func (c *Client) CreateStorage(ctx context.Context, req CreateStorageRequest) (StorageInfo, error) {
	var out StorageInfo
	err := c.do(ctx, http.MethodPost, "/storages", req, &out)
	if err != nil {
		return StorageInfo{}, err
	}
	return out, nil
}

// ListStorages implements GET /storages.
func (c *Client) ListStorages(ctx context.Context) ([]StorageInfo, error) {
	var out []StorageInfo
	err := c.do(ctx, http.MethodGet, "/storages", nil, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveStorage implements DELETE /storages/{id}.
func (c *Client) RemoveStorage(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/storages/"+id, nil, nil)
}

// patchStorageRequest mirrors internal/api's patchStorageRequest, sending
// only the retention_days field this package's callers need.
type patchStorageRequest struct {
	RetentionDays *int64 `json:"retention_days,omitempty"`
}

// SetRetentionDays implements PATCH /storages/{id} (retention_days only).
func (c *Client) SetRetentionDays(ctx context.Context, id string, days int64) error {
	return c.do(ctx, http.MethodPatch, "/storages/"+id, patchStorageRequest{RetentionDays: &days}, nil)
}

// CreateChannelCapturePolicy is CreateChannelRequest's nested capture_policy
// object -- mirrors internal/api's channelCapturePolicyRequest (flat
// type/max_deferred_start_ns/prerecord_ns/postrecord_ns, distinct from
// SetCapturePolicyRequest's nested "params" shape below).
type CreateChannelCapturePolicy struct {
	Type               string `json:"type"`
	MaxDeferredStartNS uint64 `json:"max_deferred_start_ns,omitempty"`
	PrerecordNS        uint64 `json:"prerecord_ns,omitempty"`
	PostrecordNS       uint64 `json:"postrecord_ns,omitempty"`
}

// CreateChannelRequest is POST /channels' body.
type CreateChannelRequest struct {
	ID            uint16                     `json:"id"`
	RTSPURL       string                     `json:"rtsp_url"`
	Storage       string                     `json:"storage"`
	CapturePolicy CreateChannelCapturePolicy `json:"capture_policy"`
	Name          string                     `json:"name,omitempty"`
}

// ChannelInfo mirrors internal/api's channelInfo (GET /channels' listing
// shape, and POST/PUT /channels' response shape).
type ChannelInfo struct {
	Channel      uint16 `json:"channel"`
	RTSPURL      string `json:"rtsp_url"`
	Storage      string `json:"storage"`
	PolicyType   string `json:"capture_policy_type"`
	PrerecordNS  uint64 `json:"prerecord_ns"`
	PostrecordNS uint64 `json:"postrecord_ns"`
	Name         string `json:"name,omitempty"`
}

// CreateChannel implements POST /channels.
func (c *Client) CreateChannel(ctx context.Context, req CreateChannelRequest) (ChannelInfo, error) {
	var out ChannelInfo
	err := c.do(ctx, http.MethodPost, "/channels", req, &out)
	if err != nil {
		return ChannelInfo{}, err
	}
	return out, nil
}

// RemoveChannel implements DELETE /channels/{id}.
func (c *Client) RemoveChannel(ctx context.Context, id uint16) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/channels/%d", id), nil, nil)
}

// ListChannels implements GET /channels, optionally filtered by storage
// (empty means every channel).
func (c *Client) ListChannels(ctx context.Context, storageID string) ([]ChannelInfo, error) {
	path := "/channels"
	if storageID != "" {
		path += "?storage=" + storageID
	}
	var out []ChannelInfo
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FindChannel looks up one channel by id via ListChannels -- farcd has no
// single-channel GET route, mirroring how internal/api/archives.go's own
// (now removed) findChannel used to scan IngestManager.List() in-process.
func (c *Client) FindChannel(ctx context.Context, id uint16) (ChannelInfo, bool, error) {
	list, err := c.ListChannels(ctx, "")
	if err != nil {
		return ChannelInfo{}, false, err
	}
	for _, ch := range list {
		if ch.Channel == id {
			return ch, true, nil
		}
	}
	return ChannelInfo{}, false, nil
}

// SetCapturePolicyRequest is POST /channels/{id}/capture-policy's body --
// mirrors internal/api's setCapturePolicyRequest's nested "params" shape,
// distinct from CreateChannelCapturePolicy above.
type SetCapturePolicyRequest struct {
	Type         string
	PrerecordNS  uint64
	PostrecordNS uint64
}

func (r SetCapturePolicyRequest) wire() any {
	return struct {
		Type   string `json:"type"`
		Params struct {
			PrerecordNS  uint64 `json:"prerecord_ns"`
			PostrecordNS uint64 `json:"postrecord_ns"`
		} `json:"params"`
	}{
		Type: r.Type,
		Params: struct {
			PrerecordNS  uint64 `json:"prerecord_ns"`
			PostrecordNS uint64 `json:"postrecord_ns"`
		}{PrerecordNS: r.PrerecordNS, PostrecordNS: r.PostrecordNS},
	}
}

// SetCapturePolicy implements POST /channels/{id}/capture-policy.
func (c *Client) SetCapturePolicy(ctx context.Context, id uint16, req SetCapturePolicyRequest) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/channels/%d/capture-policy", id), req.wire(), nil)
}

// StartRecording implements POST /channels/{id}/recording/start.
// fromTimeNS is optional (nil omits from_time_ns from the body).
func (c *Client) StartRecording(ctx context.Context, id uint16, fromTimeNS *uint64) error {
	body := struct {
		FromTimeNS *uint64 `json:"from_time_ns,omitempty"`
	}{FromTimeNS: fromTimeNS}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/channels/%d/recording/start", id), body, nil)
}

// StopRecording implements POST /channels/{id}/recording/stop.
func (c *Client) StopRecording(ctx context.Context, id uint16) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/channels/%d/recording/stop", id), nil, nil)
}
