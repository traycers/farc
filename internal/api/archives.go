// Package api's archives.go implements /api/v1/archives/* from
// temp/controller/openapi.yaml (the msm-tagged operations msm calls into
// farcd with) -- an "archive" in that spec maps 1:1 onto this package's
// existing Storage (see docs/docs/archive/11-service-composition.md's
// unrelated, process-wide use of "Archive" -- this route group ignores that
// meaning and treats {aid} as a Storage id). Every handler here is a thin
// wrapper composing the same primitives storages.go/channels.go already
// expose (createStorage/removeStorage/createChannel/removeChannel/
// SetPolicy/StartRecording/StopRecording), batched over archive-shaped
// request bodies. Error bodies on this route group are {"code","message"}
// (models.errors.Error), not this package's usual {"error":"..."} --
// writeArchiveError/writeArchiveAPIError below, not writeError.
package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/ingest"
	"traycers/farc/internal/storage"
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
	// equivalent of channelCapturePolicyRequest.MaxDeferredStartNS, and
	// every archive-managed channel is always "continuous" (there's no
	// capture_policy.type field in the archive channel spec either).
	defaultArchiveMaxDeferredStartNS = uint64(30 * time.Second)
)

// archiveErrorBody is models.errors.Error: {code,message}, distinct from
// this package's usual writeError shape.
type archiveErrorBody struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

func writeArchiveError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, archiveErrorBody{Code: int32(status), Message: message})
}

// writeArchiveAPIError renders err (typically returned by createStorage/
// createChannel/removeChannel/removeStorage) in this route group's
// {code,message} shape, honoring the status an *apiError requested and
// falling back to 500 otherwise -- every call site here is composing several
// already-successful steps, so an unwrapped error at this point is always
// this package's own downstream failure, never a client one.
func writeArchiveAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.status
		err = ae.err
	}
	writeArchiveError(w, status, err.Error())
}

func secondsToNS(s int64) uint64 { return uint64(s) * uint64(time.Second) }

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

// createArchiveChannel adapts c into a createChannelRequest against aid and
// delegates to createChannel -- shared by archives_setup and channels_add,
// neither of which reports per-channel info back to the caller (unlike
// POST /channels' 201 body), hence just error rather than createChannel's
// (channelInfo, error).
func (s *HttpApiServer) createArchiveChannel(aid string, c archiveChannelConfigNew) error {
	if len(c.URLs) == 0 {
		return apiErr(http.StatusBadRequest, fmt.Errorf("api: channel %d: urls is required", c.Num))
	}
	req := createChannelRequest{
		ID:      c.Num,
		RTSPURL: c.URLs[0],
		Storage: aid,
		CapturePolicy: channelCapturePolicyRequest{
			Type:               "continuous",
			MaxDeferredStartNS: defaultArchiveMaxDeferredStartNS,
			PrerecordNS:        secondsToNS(c.PreRecord),
			PostrecordNS:       secondsToNS(c.PostRecord),
		},
	}
	_, err := s.createChannel(req)
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
// the Storage (createStorage), set its retention from ttl, then add every
// listed channel (createArchiveChannel) -- the same three steps an operator
// would otherwise make as three separate requests (POST /storages, PATCH
// /storages/{id}, POST /channels per channel). FCount ("fblocks_count",
// legacy "количество фблоков в озу") is accepted but ignored -- farc's
// storage engine has no matching RAM-buffer-depth knob (EngineTuning is
// warning/backpressure queue thresholds, unrelated). There's no rollback
// across steps: a failure partway through (e.g. the 2nd of 3 channels)
// leaves the Storage and any already-added channels in place, same as
// running the equivalent requests by hand would.
func (s *HttpApiServer) handleArchiveSetup(w http.ResponseWriter, r *http.Request) {
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
	createReq := createStorageRequest{
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
	_, err = s.createStorage(createReq)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}

	if s.ing != nil {
		for _, c := range req.Channels {
			err := s.createArchiveChannel(req.ID, c)
			if err != nil {
				writeArchiveAPIError(w, err)
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, struct {
		FblocksCount int64 `json:"fblocks_count"`
	}{FblocksCount: int64(n)})
}

// handleArchiveDetach is DELETE /api/v1/archives/ (archives_detach): remove
// every channel the archive owns, then the Storage itself. onStorageRemoved
// (internal/farcd's hook) runs before removeStorage actually unregisters
// and closes the Unit, so a persist failure leaves the archive fully intact
// rather than half torn down.
func (s *HttpApiServer) handleArchiveDetach(w http.ResponseWriter, r *http.Request) {
	aid := r.URL.Query().Get("id")
	if aid == "" {
		writeArchiveError(w, http.StatusBadRequest, "id is required")
		return
	}
	if _, ok := s.reg.Get(aid); !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return
	}

	if s.ing != nil {
		for _, c := range s.ing.List() {
			if c.StorageID != aid {
				continue
			}
			err := s.removeChannel(c.Channel)
			if err != nil {
				writeArchiveAPIError(w, err)
				return
			}
		}
	}

	err := s.onStorageRemoved(aid)
	if err != nil {
		writeArchiveAPIError(w, fmt.Errorf("api: persist archive %q detach: %w", aid, err))
		return
	}
	err = s.removeStorage(aid)
	if err != nil {
		writeArchiveAPIError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsAdd is POST /api/v1/archives/{aid}/channels/.
func (s *HttpApiServer) handleArchiveChannelsAdd(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeArchiveError(w, http.StatusNotImplemented, errNoIngestManager.Error())
		return
	}
	aid := r.PathValue("aid")
	if _, ok := s.reg.Get(aid); !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return
	}
	var req []archiveChannelConfigNew
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, c := range req {
		err := s.createArchiveChannel(aid, c)
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsDel is DELETE /api/v1/archives/{aid}/channels/.
// aid itself is only used to 404 an unknown archive early -- removeChannel
// doesn't otherwise care which archive a channel id belongs to (channel ids
// are globally unique in IngestManager, same as DELETE /channels/{id}).
func (s *HttpApiServer) handleArchiveChannelsDel(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeArchiveError(w, http.StatusNotImplemented, errNoIngestManager.Error())
		return
	}
	aid := r.PathValue("aid")
	if _, ok := s.reg.Get(aid); !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
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
		err = s.removeChannel(uint16(ch))
		if err != nil {
			writeArchiveAPIError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleArchiveChannelsConfigUpdate is
// PATCH /api/v1/archives/{aid}/channels/config/ (config_update): unlike
// PUT /channels/{id}, this only ever touches pre_record/post_record, so it
// goes through IngestManager.SetPolicy (in-place param update) rather than
// remove-then-recreate -- the latter would need the channel's current
// max_deferred_start_ns, which channelInfo/ChannelInfo doesn't expose.
func (s *HttpApiServer) handleArchiveChannelsConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeArchiveError(w, http.StatusNotImplemented, errNoIngestManager.Error())
		return
	}
	aid := r.PathValue("aid")
	if _, ok := s.reg.Get(aid); !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return
	}
	var req []archiveChannelConfig
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, c := range req {
		info, ok := s.findChannel(c.Num)
		if !ok {
			writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown channel %d", c.Num))
			return
		}
		err := s.ing.SetPolicy(c.Num, info.PolicyType, ingest.PolicyParams{
			Prerecord: secondsToNS(c.PreRecord), Postrecord: secondsToNS(c.PostRecord),
		})
		if err != nil {
			writeArchiveError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

type archiveRecordingRequest struct {
	Channels []uint16 `json:"channels"`
}

// handleArchiveRecording backs both recording_start and recording_stop
// (POST /api/v1/archives/{aid}/recording/{start,stop}) -- start selects
// which of IngestManager's StartRecording/StopRecording runs per channel,
// same underlying calls as the existing single-channel
// POST /channels/{id}/recording/start|stop routes.
func (s *HttpApiServer) handleArchiveRecording(w http.ResponseWriter, r *http.Request, start bool) {
	if s.ing == nil {
		writeArchiveError(w, http.StatusNotImplemented, errNoIngestManager.Error())
		return
	}
	aid := r.PathValue("aid")
	if _, ok := s.reg.Get(aid); !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return
	}
	var req archiveRecordingRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := uint64(time.Now().UnixNano())
	for _, ch := range req.Channels {
		if start {
			err = s.ing.StartRecording(ch, now, nil)
		} else {
			err = s.ing.StopRecording(ch, now)
		}
		if err != nil {
			status := http.StatusNotFound
			if errors.Is(err, ingest.ErrWrongPolicyType) {
				status = http.StatusConflict
			}
			writeArchiveError(w, status, err.Error())
			return
		}
		if s.push != nil {
			name := EventRecordingCommandStop
			if start {
				name = EventRecordingCommandStart
			}
			s.push.Publish(JournalEvent{Name: name, Channel: ch})
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *HttpApiServer) handleArchiveRecordingStart(w http.ResponseWriter, r *http.Request) {
	s.handleArchiveRecording(w, r, true)
}

func (s *HttpApiServer) handleArchiveRecordingStop(w http.ResponseWriter, r *http.Request) {
	s.handleArchiveRecording(w, r, false)
}

type archiveTTLRequest struct {
	TTL int64 `json:"ttl"`
}

// handleArchiveTTLSet is PUT /api/v1/archives/{aid}/ttl/ -- ttl (days) is
// exactly PATCH /storages/{id}'s retention_days, just always-required
// instead of optional.
func (s *HttpApiServer) handleArchiveTTLSet(w http.ResponseWriter, r *http.Request) {
	aid := r.PathValue("aid")
	unit, ok := s.reg.Get(aid)
	if !ok {
		writeArchiveError(w, http.StatusNotFound, fmt.Sprintf("unknown archive %q", aid))
		return
	}
	var req archiveTTLRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeArchiveError(w, http.StatusBadRequest, err.Error())
		return
	}
	unit.Index().SetRetentionDays(req.TTL)
	w.WriteHeader(http.StatusOK)
}
