package apid

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Server is apid's HTTP API -- the web app's single write path for
// channels (.scratch/live-page/issues/01-apid-server.md). Routes use
// stdlib net/http.ServeMux's method+{param} patterns, matching
// internal/api/server.go's own router style.
type Server struct {
	orch *Orchestrator
	mux  *http.ServeMux
}

// NewServer creates a Server wired to orch.
func NewServer(orch *Orchestrator) *Server {
	s := &Server{orch: orch, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns s's http.Handler, e.g. for httptest.NewServer in tests
// or http.Server.Handler in cmd/apid.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /channels/{id}", s.handleGetChannel)
	s.mux.HandleFunc("POST /channels", s.handleCreateChannel)
	s.mux.HandleFunc("PATCH /channels/{id}", s.handleUpdateChannel)
	s.mux.HandleFunc("DELETE /channels/{id}", s.handleRemoveChannel)
	s.mux.HandleFunc("GET /channels/live-urls", s.handleGetLiveURLs)
}

// writeCapturePolicy is the wire shape of a create/update request's nested
// capture_policy object -- deliberately identical to farcd's own
// channelCapturePolicyRequest (internal/api/channels.go) so the web app's
// existing create/update form payloads need no reshaping when they switch
// from calling farcd directly to calling apid
// (.scratch/live-page/issues/03-web-channel-writes-via-apid.md).
type writeCapturePolicy struct {
	Type               string `json:"type"`
	MaxDeferredStartNS uint64 `json:"max_deferred_start_ns,omitempty"`
	PrerecordNS        uint64 `json:"prerecord_ns,omitempty"`
	PostrecordNS       uint64 `json:"postrecord_ns,omitempty"`
}

// createChannelRequest is POST /channels' body. RTSPURL is the camera's
// real RTSP URL -- from the web app's perspective this is exactly the
// same field it already sends to farcd today; apid, not the web app,
// knows it actually needs rewriting before it reaches farcd.
type createChannelRequest struct {
	ID            uint16             `json:"id"`
	RTSPURL       string             `json:"rtsp_url"`
	Storage       string             `json:"storage"`
	CapturePolicy writeCapturePolicy `json:"capture_policy"`
	Name          string             `json:"name,omitempty"`
}

// updateChannelRequest is PATCH /channels/{id}'s body -- same fields as
// create, minus ID (taken from the path).
type updateChannelRequest struct {
	RTSPURL       string             `json:"rtsp_url"`
	Storage       string             `json:"storage"`
	CapturePolicy writeCapturePolicy `json:"capture_policy"`
	Name          string             `json:"name,omitempty"`
}

// writeResultResponse is create/update/remove's response body: each side's
// outcome, independently -- ".scratch/live-page/issues/01-apid-server.md"'s
// partial-failure contract, e.g. {"farcd":"ok","mediamtx":"error: ..."}.
type writeResultResponse struct {
	Farcd    string `json:"farcd"`
	Mediamtx string `json:"mediamtx"`
}

// toResponse renders a WriteResult as the wire shape above, plus the HTTP
// status to use: 200 if both sides succeeded, 207 Multi-Status if either
// (or both) failed -- the body always carries which.
func toResponse(r WriteResult) (writeResultResponse, int) {
	resp := writeResultResponse{}
	if r.FarcdOK {
		resp.Farcd = "ok"
	} else {
		resp.Farcd = fmt.Sprintf("error: %v", r.FarcdErr)
	}
	if r.MediamtxOK {
		resp.Mediamtx = "ok"
	} else {
		resp.Mediamtx = fmt.Sprintf("error: %v", r.MediamtxErr)
	}
	status := http.StatusOK
	if !r.FarcdOK || !r.MediamtxOK {
		status = http.StatusMultiStatus
	}
	return resp, status
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeBadRequest(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, err)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err != nil {
		return fmt.Errorf("apid: decode request body: %w", err)
	}
	return nil
}

func parseChannelID(r *http.Request) (uint16, error) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("apid: invalid channel id %q: %w", r.PathValue("id"), err)
	}
	return uint16(id), nil
}

// handleGetChannel implements GET /channels/{id}: today, just the
// channel's camera RTSP URL (read back from mediamtx, since apid never
// persists it itself) -- the web app's edit-channel form needs this to
// prefill rtsp_url, because farcd's own stored rtsp_url for this channel
// is mediamtx's re-serve address, not the camera's
// (.scratch/live-page/issues/01-apid-server.md).
func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id, err := parseChannelID(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	source, exists, err := s.orch.GetCameraURL(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("apid: channel %d: no mediamtx path configured", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"camera_rtsp_url": source})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var req createChannelRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	result := s.orch.CreateChannel(r.Context(), ChannelWriteRequest{
		ID:                 req.ID,
		CameraRTSPURL:      req.RTSPURL,
		Storage:            req.Storage,
		CapturePolicyType:  req.CapturePolicy.Type,
		MaxDeferredStartNS: req.CapturePolicy.MaxDeferredStartNS,
		PrerecordNS:        req.CapturePolicy.PrerecordNS,
		PostrecordNS:       req.CapturePolicy.PostrecordNS,
		Name:               req.Name,
	})
	resp, status := toResponse(result)
	if status == http.StatusOK {
		status = http.StatusCreated
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := parseChannelID(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	var req updateChannelRequest
	err = decodeJSON(r, &req)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	result := s.orch.UpdateChannel(r.Context(), id, ChannelWriteRequest{
		ID:                 id,
		CameraRTSPURL:      req.RTSPURL,
		Storage:            req.Storage,
		CapturePolicyType:  req.CapturePolicy.Type,
		MaxDeferredStartNS: req.CapturePolicy.MaxDeferredStartNS,
		PrerecordNS:        req.CapturePolicy.PrerecordNS,
		PostrecordNS:       req.CapturePolicy.PostrecordNS,
		Name:               req.Name,
	})
	resp, status := toResponse(result)
	writeJSON(w, status, resp)
}

func (s *Server) handleRemoveChannel(w http.ResponseWriter, r *http.Request) {
	id, err := parseChannelID(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	result := s.orch.RemoveChannel(r.Context(), id)
	resp, status := toResponse(result)
	writeJSON(w, status, resp)
}

// handleGetLiveURLs implements GET /channels/live-urls?ids=1,2,3 -- a
// single batch lookup, never one request per checked channel
// (.scratch/live-page/issues/01-apid-server.md).
func (s *Server) handleGetLiveURLs(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ids")
	if raw == "" {
		writeJSON(w, http.StatusOK, map[string]map[string]string{"urls": {}})
		return
	}
	parts := splitComma(raw)
	ids := make([]uint16, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			writeBadRequest(w, fmt.Errorf("apid: invalid channel id %q in ids: %w", p, err))
			return
		}
		ids = append(ids, uint16(id))
	}
	urls := s.orch.GetLiveURLs(ids)
	out := make(map[string]string, len(urls))
	for id, url := range urls {
		out[strconv.Itoa(int(id))] = url
	}
	writeJSON(w, http.StatusOK, map[string]map[string]string{"urls": out})
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
