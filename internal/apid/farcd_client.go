// Package apid implements apid: the web app's single write path for
// channels, fanning create/update/remove out to farcd and mediamtx so
// that mediamtx is the only thing that ever opens an RTSP session against
// a camera (farcd's own ingest pulls the re-served stream from mediamtx
// instead) -- see .scratch/live-page/spec.md for the full design.
package apid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChannelInfo mirrors internal/api's channelInfo (GET/POST/PUT /channels'
// response shape) -- this package defines its own copy rather than
// importing internal/api's unexported type, the same reasoning
// internal/hlsclient's own doc comment gives: an HTTP wire contract, not a
// Go API to depend on directly.
type ChannelInfo struct {
	Channel          uint16
	RTSPURL          string
	Storage          string
	PolicyType       string
	PrerecordNS      uint64
	PostrecordNS     uint64
	Name             string
	Connected        bool
	Recording        bool
	LastConnectError string
}

// CreateChannelRequest is FarcdClient.CreateChannel's input -- the same
// fields farcd's own POST /channels accepts (internal/api/channels.go's
// createChannelRequest), flattened (no nested capture-policy struct) since
// nothing in this package needs the nesting.
type CreateChannelRequest struct {
	ID                 uint16
	RTSPURL            string
	Storage            string
	CapturePolicyType  string
	MaxDeferredStartNS uint64
	PrerecordNS        uint64
	PostrecordNS       uint64
	Name               string
}

// UpdateChannelRequest is FarcdClient.UpdateChannel's input, mirroring
// farcd's PUT /channels/{id} (internal/api/channels.go's
// updateChannelRequest).
type UpdateChannelRequest struct {
	RTSPURL            string
	Storage            string
	CapturePolicyType  string
	MaxDeferredStartNS uint64
	PrerecordNS        uint64
	PostrecordNS       uint64
	Name               string
}

// FarcdClient is the subset of farcd's channel-CRUD REST API apid's
// orchestrator (orchestrator.go) depends on -- an interface so tests can
// substitute a fake instead of a real farcd.
type FarcdClient interface {
	ListChannels(ctx context.Context) ([]ChannelInfo, error)
	CreateChannel(ctx context.Context, req CreateChannelRequest) (ChannelInfo, error)
	UpdateChannel(ctx context.Context, id uint16, req UpdateChannelRequest) (ChannelInfo, error)
	RemoveChannel(ctx context.Context, id uint16) error
}

// FarcdHTTPClient is FarcdClient's real implementation, talking to a real
// farcd over HTTP.
type FarcdHTTPClient struct {
	base string
	hc   *http.Client
}

// NewFarcdClient creates a FarcdHTTPClient. base is farcd's HTTP API base
// URL with no trailing slash requirement, e.g. "http://farc:8080".
func NewFarcdClient(base string) *FarcdHTTPClient {
	return &FarcdHTTPClient{base: strings.TrimSuffix(base, "/"), hc: &http.Client{}}
}

// wireChannelInfo mirrors internal/api's channelInfo.
type wireChannelInfo struct {
	Channel          uint16 `json:"channel"`
	RTSPURL          string `json:"rtsp_url"`
	Storage          string `json:"storage"`
	PolicyType       string `json:"capture_policy_type"`
	PrerecordNS      uint64 `json:"prerecord_ns"`
	PostrecordNS     uint64 `json:"postrecord_ns"`
	Name             string `json:"name,omitempty"`
	Connected        bool   `json:"connected"`
	Recording        bool   `json:"recording"`
	LastConnectError string `json:"last_connect_error,omitempty"`
}

func (w wireChannelInfo) toChannelInfo() ChannelInfo { return ChannelInfo(w) }

// wireCapturePolicy mirrors internal/api's channelCapturePolicyRequest.
type wireCapturePolicy struct {
	Type               string `json:"type"`
	MaxDeferredStartNS uint64 `json:"max_deferred_start_ns,omitempty"`
	PrerecordNS        uint64 `json:"prerecord_ns,omitempty"`
	PostrecordNS       uint64 `json:"postrecord_ns,omitempty"`
}

type wireCreateChannelRequest struct {
	ID            uint16            `json:"id"`
	RTSPURL       string            `json:"rtsp_url"`
	Storage       string            `json:"storage"`
	CapturePolicy wireCapturePolicy `json:"capture_policy"`
	Name          string            `json:"name,omitempty"`
}

type wireUpdateChannelRequest struct {
	RTSPURL       string            `json:"rtsp_url"`
	Storage       string            `json:"storage"`
	CapturePolicy wireCapturePolicy `json:"capture_policy"`
	Name          string            `json:"name,omitempty"`
}

// do issues an HTTP request with an optional JSON body and returns the
// response body's raw bytes on success (2xx), translating farcd's
// {"error": "..."} bodies (internal/api's writeError) into a descriptive
// Go error otherwise -- same convention as internal/hlsclient.Client.do.
func (c *FarcdHTTPClient) do(ctx context.Context, method, path string, reqBody any, allow404 bool) ([]byte, error) {
	var reader io.Reader
	if reqBody != nil {
		buf, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("apid: farcd: marshal %s %s body: %w", method, path, err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("apid: farcd: build request for %s %s: %w", method, path, err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apid: farcd: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("apid: farcd: %s %s: read body: %w", method, path, err)
	}
	if allow404 && resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("apid: farcd: %s %s: %s (status %d)", method, path, e.Error, resp.StatusCode)
		}
		return nil, fmt.Errorf("apid: farcd: %s %s: status %d: %s", method, path, resp.StatusCode, string(body))
	}
	return body, nil
}

// ListChannels implements GET /channels.
func (c *FarcdHTTPClient) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	body, err := c.do(ctx, http.MethodGet, "/channels", nil, false)
	if err != nil {
		return nil, err
	}
	var wire []wireChannelInfo
	err = json.Unmarshal(body, &wire)
	if err != nil {
		return nil, fmt.Errorf("apid: farcd: GET /channels: decode response: %w", err)
	}
	out := make([]ChannelInfo, len(wire))
	for i, w := range wire {
		out[i] = w.toChannelInfo()
	}
	return out, nil
}

// CreateChannel implements POST /channels.
func (c *FarcdHTTPClient) CreateChannel(ctx context.Context, req CreateChannelRequest) (ChannelInfo, error) {
	wireReq := wireCreateChannelRequest{
		ID:      req.ID,
		RTSPURL: req.RTSPURL,
		Storage: req.Storage,
		CapturePolicy: wireCapturePolicy{
			Type:               req.CapturePolicyType,
			MaxDeferredStartNS: req.MaxDeferredStartNS,
			PrerecordNS:        req.PrerecordNS,
			PostrecordNS:       req.PostrecordNS,
		},
		Name: req.Name,
	}
	body, err := c.do(ctx, http.MethodPost, "/channels", wireReq, false)
	if err != nil {
		return ChannelInfo{}, err
	}
	var wire wireChannelInfo
	err = json.Unmarshal(body, &wire)
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("apid: farcd: POST /channels: decode response: %w", err)
	}
	return wire.toChannelInfo(), nil
}

// UpdateChannel implements PUT /channels/{id}.
func (c *FarcdHTTPClient) UpdateChannel(ctx context.Context, id uint16, req UpdateChannelRequest) (ChannelInfo, error) {
	wireReq := wireUpdateChannelRequest{
		RTSPURL: req.RTSPURL,
		Storage: req.Storage,
		CapturePolicy: wireCapturePolicy{
			Type:               req.CapturePolicyType,
			MaxDeferredStartNS: req.MaxDeferredStartNS,
			PrerecordNS:        req.PrerecordNS,
			PostrecordNS:       req.PostrecordNS,
		},
		Name: req.Name,
	}
	body, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/channels/%d", id), wireReq, false)
	if err != nil {
		return ChannelInfo{}, err
	}
	var wire wireChannelInfo
	err = json.Unmarshal(body, &wire)
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("apid: farcd: PUT /channels/%d: decode response: %w", id, err)
	}
	return wire.toChannelInfo(), nil
}

// RemoveChannel implements DELETE /channels/{id}.
func (c *FarcdHTTPClient) RemoveChannel(ctx context.Context, id uint16) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/channels/%d", id), nil, true)
	return err
}
