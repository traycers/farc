// Package archivesapi is msm_server's inbound HTTP server for
// /api/v1/archives/* (temp/controller/openapi.yaml's msm-tagged
// operations) -- the external msm/controller system's single integration
// point into this codebase, per the 2026-08-13 decision to move this route
// group out of farcd (which now exposes only its generic Storage/Channel
// primitives) and into msm_server. Every handler here is a thin translation
// layer composing farcClient (satisfied by internal/farcctl.Client) calls
// against those generic primitives, batched over archive-shaped request
// bodies -- exactly what farcd's own (now removed) internal/api/archives.go
// used to do in-process, just one HTTP hop further out. Error bodies on
// this route group are {"code","message"} (models.errors.Error), matching
// the controller spec.
package archivesapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/farcctl"
	"github.com/traycers/farc/internal/storage"
)

const (
	mib = 1024 * 1024

	// defaultArchiveFchunkSize backs every Storage created via
	// archives_setup -- models.archive.ConfigNew has no fchunk_size field,
	// so we pick a fixed v1 default from the documented 4-16MB fchunk range
	// (CLAUDE.md) rather than rejecting the request.
	defaultArchiveFchunkSize = 8 * mib

	// defaultArchiveMaxChannels backs every Storage created via
	// archives_setup -- storage.Geometry.MaxChannels is a farc-only concept
	// with no equivalent in models.archive.ConfigNew.
	defaultArchiveMaxChannels = 64

	// defaultArchiveMaxDeferredStartNS backs every channel created via
	// channels_add/archives_setup -- models.channel.ConfigNew has no
	// equivalent of a max_deferred_start_ns, and every archive-managed
	// channel is always "continuous" (there's no capture_policy.type field
	// in the archive channel spec either).
	defaultArchiveMaxDeferredStartNS = uint64(30 * time.Second)
)

// farcClient is internal/farcctl.Client's method set this package needs --
// an interface so tests can substitute a fake, though this package's own
// tests run against a real farcctl.Client backed by a real farcd instead
// (see testutil_test.go).
type farcClient interface {
	CreateStorage(ctx context.Context, req farcctl.CreateStorageRequest) (farcctl.StorageInfo, error)
	ListStorages(ctx context.Context) ([]farcctl.StorageInfo, error)
	RemoveStorage(ctx context.Context, id string) error
	SetRetentionDays(ctx context.Context, id string, days int64) error
	CreateChannel(ctx context.Context, req farcctl.CreateChannelRequest) (farcctl.ChannelInfo, error)
	RemoveChannel(ctx context.Context, id uint16) error
	ListChannels(ctx context.Context, storageID string) ([]farcctl.ChannelInfo, error)
	FindChannel(ctx context.Context, id uint16) (farcctl.ChannelInfo, bool, error)
	SetCapturePolicy(ctx context.Context, id uint16, req farcctl.SetCapturePolicyRequest) error
	StartRecording(ctx context.Context, id uint16, fromTimeNS *uint64) error
	StopRecording(ctx context.Context, id uint16) error
}

// Server is /api/v1/archives/*'s HTTP handler set.
type Server struct {
	client farcClient
	mux    *http.ServeMux
}

// New builds a Server calling client for every operation.
func New(client farcClient) *Server {
	s := &Server{client: client, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns s's http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

// routes uses "{$}" on every pattern ending in a literal "/" -- without it,
// stdlib net/http.ServeMux treats a trailing "/" as a subtree prefix (it'd
// match "/api/v1/archives/garbage" as archives_setup, or silently absorb an
// extra path segment into channels_add) rather than gorilla/mux's exact-path
// behavior this route group's callers (temp/controller/openapi.yaml's
// msm/controller) were built against.
func (s *Server) routes() {
	s.mux.HandleFunc("PUT /api/v1/archives/{$}", s.handleArchiveSetup)
	s.mux.HandleFunc("DELETE /api/v1/archives/{$}", s.handleArchiveDetach)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/channels/{$}", s.handleArchiveChannelsAdd)
	s.mux.HandleFunc("DELETE /api/v1/archives/{aid}/channels/{$}", s.handleArchiveChannelsDel)
	s.mux.HandleFunc("PATCH /api/v1/archives/{aid}/channels/config/{$}", s.handleArchiveChannelsConfigUpdate)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/recording/start", s.handleArchiveRecordingStart)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/recording/stop", s.handleArchiveRecordingStop)
	s.mux.HandleFunc("PUT /api/v1/archives/{aid}/ttl/{$}", s.handleArchiveTTLSet)
}

// archiveErrorBody is models.errors.Error: {code,message}.
type archiveErrorBody struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

func writeArchiveError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, archiveErrorBody{Code: int32(status), Message: message})
}

// writeArchiveAPIError renders err (typically a *farcctl.APIError returned
// by a farcClient call) in this route group's {code,message} shape,
// honoring the status the call actually got back from farcd and falling
// back to 500 for anything else (a transport-level failure, not a farcd
// response).
func writeArchiveAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	var ae *farcctl.APIError
	if errors.As(err, &ae) {
		status = ae.Status
		message = ae.Message
	}
	writeArchiveError(w, status, message)
}

func secondsToNS(s int64) uint64 { return uint64(s) * uint64(time.Second) }

// archiveExists reports whether aid is a currently registered farcd
// Storage, via GET /storages (there is no single-resource existence check)
// -- used by the handlers whose temp/controller/openapi.yaml entry declares
// a 404 for an unknown archive (channels_del, config_update,
// recording_start/stop). archives_setup/archives_detach/ttl_set don't need
// this: they get the same 404 for free from the single-resource farcd call
// they make anyway (POST is a create so N/A, DELETE/PATCH /storages/{id}
// already 404 on an unknown id).
func archiveExists(ctx context.Context, client farcClient, aid string) (bool, error) {
	list, err := client.ListStorages(ctx)
	if err != nil {
		return false, err
	}
	for _, st := range list {
		if st.ID == aid {
			return true, nil
		}
	}
	return false, nil
}

// requireArchive 404s and returns false if aid isn't a registered farcd
// Storage, else returns true. Callers should return immediately when it
// returns false.
func requireArchive(ctx context.Context, w http.ResponseWriter, client farcClient, aid string) bool {
	ok, err := archiveExists(ctx, client, aid)
	if err != nil {
		writeArchiveAPIError(w, err)
		return false
	}
	if !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return false
	}
	return true
}

// archiveChannelConfigNew is models.channel.ConfigNew: a channel as
// described by archives_setup/channels_add. Only urls[0] is used --
// multi-URL failover is out of scope (internal/ingest has no notion of a
// backup RTSP source for one channel).
type archiveChannelConfigNew struct {
	Num        uint16   `json:"num"`
	PreRecord  int64    `json:"pre_record"`
	PostRecord int64    `json:"post_record"`
	URLs       []string `json:"urls"`
}

// archiveChannelConfig is models.channel.Config: config_update's body,
// missing urls (only pre_record/post_record are mutable that way).
type archiveChannelConfig struct {
	Num        uint16 `json:"num"`
	PreRecord  int64  `json:"pre_record"`
	PostRecord int64  `json:"post_record"`
}

// createArchiveChannel adapts c into a farcctl.CreateChannelRequest against
// aid -- shared by archives_setup and channels_add, neither of which
// reports per-channel info back to the caller.
func (s *Server) createArchiveChannel(ctx context.Context, aid string, c archiveChannelConfigNew) error {
	if len(c.URLs) == 0 {
		return &farcctl.APIError{Status: http.StatusBadRequest, Message: fmt.Sprintf("channel %d: urls is required", c.Num)}
	}
	req := farcctl.CreateChannelRequest{
		ID:      c.Num,
		RTSPURL: c.URLs[0],
		Storage: aid,
		CapturePolicy: farcctl.CreateChannelCapturePolicy{
			Type:               "continuous",
			MaxDeferredStartNS: defaultArchiveMaxDeferredStartNS,
			PrerecordNS:        secondsToNS(c.PreRecord),
			PostrecordNS:       secondsToNS(c.PostRecord),
		},
	}
	_, err := s.client.CreateChannel(ctx, req)
	return err
}

// archiveConfigNew is models.archive.ConfigNew, archives_setup's body.
type archiveConfigNew struct {
	ID       string                    `json:"id"`
	TTL      int64                     `json:"ttl"`
	Path     string                    `json:"path"`
	Size     int64                     `json:"size"`         // total, MB
	FSize    int64                     `json:"fblocks_size"` // per fblock, MB
	FCount   int64                     `json:"fblocks_count"`
	Channels []archiveChannelConfigNew `json:"channels"`
}

// handleArchiveSetup is PUT /api/v1/archives/ (archives_setup): create+init
// the Storage (CreateStorage), then add every listed channel
// (createArchiveChannel) -- the same steps an operator would otherwise make
// as separate requests against farcd (POST /storages, POST /channels per
// channel). FCount ("fblocks_count", legacy "количество фблоков в озу") is
// accepted but ignored -- farc's storage engine has no matching
// RAM-buffer-depth knob. There's no rollback across steps: a failure
// partway through (e.g. the 2nd of 3 channels) leaves the Storage and any
// already-added channels in place, same as running the equivalent requests
// by hand would.
func (s *Server) handleArchiveSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req archiveConfigNew
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" || req.Path == "" || req.FSize <= 0 {
		writeArchiveError(w, http.StatusBadRequest, "id, path and fblocks_size are required")
		return
	}

	n := uint32(req.Size / req.FSize)
	createReq := farcctl.CreateStorageRequest{
		ID:   req.ID,
		Path: req.Path,
		Geometry: storage.Geometry{
			FblockSize:  uint64(req.FSize) * mib,
			N:           n,
			MaxChannels: defaultArchiveMaxChannels,
		},
		Params: fblock.Params{
			FchunkSize: defaultArchiveFchunkSize,
			WriteMode:  fblock.WriteModeCyclic,
			Retention:  fblock.Retention{Days: req.TTL},
		},
	}
	_, err = s.client.CreateStorage(ctx, createReq)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}

	for _, c := range req.Channels {
		err := s.createArchiveChannel(ctx, req.ID, c)
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		FblocksCount int64 `json:"fblocks_count"`
	}{FblocksCount: int64(n)})
}

// handleArchiveDetach is DELETE /api/v1/archives/ (archives_detach): remove
// every channel the archive owns, then the Storage itself. An unknown aid
// surfaces as whatever status RemoveStorage's underlying farcd call
// returns (404) -- ListChannels(aid) for an unknown aid simply returns no
// channels, so no channel-removal side effects happen either way.
func (s *Server) handleArchiveDetach(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aid := r.URL.Query().Get("id")
	if aid == "" {
		writeArchiveError(w, http.StatusBadRequest, "id is required")
		return
	}

	channels, err := s.client.ListChannels(ctx, aid)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}
	for _, c := range channels {
		err := s.client.RemoveChannel(ctx, c.Channel)
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}

	err = s.client.RemoveStorage(ctx, aid)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsAdd is POST /api/v1/archives/{aid}/channels/. An
// unknown aid surfaces as the 400 farcd's own CreateChannel returns for an
// unknown storage -- channels_add's spec entry declares 400/500, not 404,
// so this needs no separate existence check.
func (s *Server) handleArchiveChannelsAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aid := r.PathValue("aid")
	var req []archiveChannelConfigNew
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, c := range req {
		err := s.createArchiveChannel(ctx, aid, c)
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsDel is DELETE /api/v1/archives/{aid}/channels/.
func (s *Server) handleArchiveChannelsDel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aid := r.PathValue("aid")
	if !requireArchive(ctx, w, s.client, aid) {
		return
	}
	ids := r.URL.Query()["id"]
	if len(ids) == 0 {
		writeArchiveError(w, http.StatusBadRequest, "id is required")
		return
	}
	for _, idStr := range ids {
		ch, err := strconv.ParseUint(idStr, 10, 16)
		if err != nil {
			writeArchiveError(w, http.StatusBadRequest, fmt.Sprintf("invalid channel id %q: %v", idStr, err))
			return
		}
		err = s.client.RemoveChannel(ctx, uint16(ch))
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsConfigUpdate is
// PATCH /api/v1/archives/{aid}/channels/config/ (config_update): unlike a
// hypothetical full channel replace, this only ever touches
// pre_record/post_record, so it goes through FindChannel+SetCapturePolicy
// (in-place param update) rather than remove-then-recreate.
func (s *Server) handleArchiveChannelsConfigUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aid := r.PathValue("aid")
	if !requireArchive(ctx, w, s.client, aid) {
		return
	}
	var req []archiveChannelConfig
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, c := range req {
		info, ok, err := s.client.FindChannel(ctx, c.Num)
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
		if !ok {
			writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown channel %d", c.Num))
			return
		}
		err = s.client.SetCapturePolicy(ctx, c.Num, farcctl.SetCapturePolicyRequest{
			Type: info.PolicyType, PrerecordNS: secondsToNS(c.PreRecord), PostrecordNS: secondsToNS(c.PostRecord),
		})
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

type archiveRecordingRequest struct {
	Channels []uint16 `json:"channels"`
}

// handleArchiveRecording backs both recording_start and recording_stop
// (POST /api/v1/archives/{aid}/recording/{start,stop}).
func (s *Server) handleArchiveRecording(w http.ResponseWriter, r *http.Request, start bool) {
	ctx := r.Context()
	aid := r.PathValue("aid")
	if !requireArchive(ctx, w, s.client, aid) {
		return
	}
	var req archiveRecordingRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, ch := range req.Channels {
		if start {
			err = s.client.StartRecording(ctx, ch, nil)
		} else {
			err = s.client.StopRecording(ctx, ch)
		}
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleArchiveRecordingStart(w http.ResponseWriter, r *http.Request) {
	s.handleArchiveRecording(w, r, true)
}

func (s *Server) handleArchiveRecordingStop(w http.ResponseWriter, r *http.Request) {
	s.handleArchiveRecording(w, r, false)
}

type archiveTTLRequest struct {
	TTL int64 `json:"ttl"`
}

// handleArchiveTTLSet is PUT /api/v1/archives/{aid}/ttl/ -- ttl (days) is
// exactly SetRetentionDays, just always-required instead of optional. An
// unknown aid surfaces as the 404 farcd's own PATCH /storages/{id} returns.
func (s *Server) handleArchiveTTLSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aid := r.PathValue("aid")
	var req archiveTTLRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	err = s.client.SetRetentionDays(ctx, aid, req.TTL)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
