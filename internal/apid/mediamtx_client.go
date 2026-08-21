package apid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MediamtxClient is the subset of mediamtx's control API
// (https://mediamtx.org/docs/features/control-api) apid's orchestrator
// depends on to keep a path in sync with a channel's camera RTSP source.
// An interface so tests can substitute a fake instead of a real mediamtx
// (this repo has no real mediamtx binary to embed in tests, unlike farcd --
// see mediamtx_client_test.go's hand-rolled fake server).
type MediamtxClient interface {
	// PathExists reports whether a path config named name already exists
	// (GET /v3/config/paths/get/{name}), so the orchestrator can decide
	// AddPath vs PatchPath idempotently instead of relying on AddPath's
	// error status alone (.scratch/live-page/issues/01-apid-server.md).
	PathExists(ctx context.Context, name string) (bool, error)
	// GetPathSource returns the path's currently configured source (the
	// channel's camera RTSP URL) -- the only place apid can read this back
	// from, since it never persists it itself.
	GetPathSource(ctx context.Context, name string) (source string, exists bool, err error)
	AddPath(ctx context.Context, name, source string) error
	PatchPath(ctx context.Context, name, source string) error
	DeletePath(ctx context.Context, name string) error
}

// MediamtxHTTPClient is MediamtxClient's real implementation.
type MediamtxHTTPClient struct {
	base string
	hc   *http.Client
}

// NewMediamtxClient creates a MediamtxHTTPClient. base is mediamtx's
// control-API base URL, e.g. "http://mediamtx:9997".
func NewMediamtxClient(base string) *MediamtxHTTPClient {
	return &MediamtxHTTPClient{base: strings.TrimSuffix(base, "/"), hc: &http.Client{}}
}

// do issues an HTTP request with an optional JSON body, translating
// mediamtx's {"error": "...", "status": "error"} bodies into a descriptive
// Go error on any status >= 300 except the ones the caller explicitly
// wants to see (allow404 -- PathExists' own "not found" case).
func (c *MediamtxHTTPClient) do(ctx context.Context, method, path string, reqBody any, allow404 bool) (status int, respBody []byte, _ error) {
	var reader io.Reader
	if reqBody != nil {
		buf, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("apid: mediamtx: marshal %s %s body: %w", method, path, err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("apid: mediamtx: build request for %s %s: %w", method, path, err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("apid: mediamtx: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("apid: mediamtx: %s %s: read body: %w", method, path, err)
	}
	if allow404 && resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, body, nil
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return resp.StatusCode, body, fmt.Errorf("apid: mediamtx: %s %s: %s (status %d)", method, path, e.Error, resp.StatusCode)
		}
		return resp.StatusCode, body, fmt.Errorf("apid: mediamtx: %s %s: status %d: %s", method, path, resp.StatusCode, string(body))
	}
	return resp.StatusCode, body, nil
}

// PathExists implements GET /v3/config/paths/get/{name}.
func (c *MediamtxHTTPClient) PathExists(ctx context.Context, name string) (bool, error) {
	status, _, err := c.do(ctx, http.MethodGet, "/v3/config/paths/get/"+url.PathEscape(name), nil, true)
	if err != nil {
		return false, err
	}
	return status != http.StatusNotFound, nil
}

// GetPathSource returns path name's currently configured source (the
// camera's real RTSP URL, .scratch/live-page/spec.md) via
// GET /v3/config/paths/get/{name}'s "source" field -- the only place this
// URL is stored; apid itself never persists it (see
// .scratch/live-page/issues/01-apid-server.md). exists is false, with a
// nil error, if no such path is configured.
func (c *MediamtxHTTPClient) GetPathSource(ctx context.Context, name string) (source string, exists bool, _ error) {
	status, body, err := c.do(ctx, http.MethodGet, "/v3/config/paths/get/"+url.PathEscape(name), nil, true)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	var wire struct {
		Source string `json:"source"`
	}
	err = json.Unmarshal(body, &wire)
	if err != nil {
		return "", false, fmt.Errorf("apid: mediamtx: GET /v3/config/paths/get/%s: decode response: %w", name, err)
	}
	return wire.Source, true, nil
}

// AddPath implements POST /v3/config/paths/add/{name}.
func (c *MediamtxHTTPClient) AddPath(ctx context.Context, name, source string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/v3/config/paths/add/"+url.PathEscape(name), map[string]string{"source": source}, false)
	return err
}

// PatchPath implements PATCH /v3/config/paths/patch/{name}.
func (c *MediamtxHTTPClient) PatchPath(ctx context.Context, name, source string) error {
	_, _, err := c.do(ctx, http.MethodPatch, "/v3/config/paths/patch/"+url.PathEscape(name), map[string]string{"source": source}, false)
	return err
}

// DeletePath implements DELETE /v3/config/paths/delete/{name}. Idempotent:
// deleting a path that's already gone is treated as success, matching
// FarcdHTTPClient.RemoveChannel's own idempotent-delete convention
// (.scratch/live-page/issues/01-apid-server.md).
func (c *MediamtxHTTPClient) DeletePath(ctx context.Context, name string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/v3/config/paths/delete/"+url.PathEscape(name), nil, true)
	return err
}
