package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"traycers/farc/internal/ingest"
	"traycers/farc/internal/storageengine"
)

// channelTimeout is ChannelIngest's RTSP read/write timeout for a channel
// created/edited at runtime via this package's routes -- matches
// internal/farcd's own rtspTimeout constant for channels loaded from the
// config file at startup. Not part of the documented config schema, same
// reasoning as farcd's copy: a fixed v1 default, not an undocumented knob.
const channelTimeout = 10 * time.Second

// errNoIngestManager is every channel-mutating/command handler's response
// when this HttpApiServer was built without one (api.go's own doc comment
// on why that's a valid, supported configuration) -- shared rather than
// re-allocated per handler since the message is identical everywhere.
var errNoIngestManager = errors.New("api: no IngestManager wired into this server")

// errScheduleNotImplemented is parsePolicyType's response for the one
// documented-but-unbuilt capture-policy type (10-capture-policy.md §5.3 is
// deferred past v1 -- see internal/ingest's package doc).
var errScheduleNotImplemented = errors.New("ingest: schedule CapturePolicy is not implemented in v1")

// errChannelIDReserved and errRTSPURLRequired are create/update channel's
// 400 responses for their two hand-validated required fields (everything
// else is either optional or validated downstream by IngestManager).
var errChannelIDReserved = errors.New("api: channel id 0 is reserved (ADR-014), channel ids start at 1")
var errRTSPURLRequired = errors.New("api: rtsp_url is required")

func parseChannelID(r *http.Request) (uint16, error) {
	ch, err := strconv.ParseUint(r.PathValue("id"), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("api: invalid channel id %q: %w", r.PathValue("id"), err)
	}
	return uint16(ch), nil
}

// parsePolicyTypeString is every capture_policy-type-accepting handler's
// validation core (set-policy, create channel, update channel, and
// archives.go's channels_add): ok is false for both the "not implemented"
// and "unknown" cases -- use policyTypeErr(s) to render the right status for
// whichever it was.
func parsePolicyTypeString(s string) (ingest.PolicyType, bool) {
	switch s {
	case "continuous":
		return ingest.PolicyContinuous, true
	case "event":
		return ingest.PolicyEvent, true
	default:
		return 0, false
	}
}

// policyTypeErr renders parsePolicyTypeString's failure for a given input as
// an *apiError with the right status (501 for the documented-but-unbuilt
// "schedule", 400 for anything else unrecognized).
func policyTypeErr(s string) error {
	if s == "schedule" {
		return apiErr(http.StatusNotImplemented, errScheduleNotImplemented)
	}
	return apiErr(http.StatusBadRequest, fmt.Errorf("api: unknown capture-policy type %q", s))
}

// parsePolicyType is the single-resource handlers' HTTP-writing wrapper
// around parsePolicyTypeString -- writes the response itself on failure so
// callers just check ok.
func parsePolicyType(w http.ResponseWriter, s string) (ingest.PolicyType, bool) {
	pt, ok := parsePolicyTypeString(s)
	if !ok {
		writeAPIError(w, policyTypeErr(s), http.StatusBadRequest)
		return 0, false
	}
	return pt, true
}

type setCapturePolicyRequest struct {
	Type   string `json:"type"`
	Params struct {
		PrerecordNS  uint64 `json:"prerecord_ns"`
		PostrecordNS uint64 `json:"postrecord_ns"`
	} `json:"params"`
}

func (s *HttpApiServer) handleSetCapturePolicy(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req setCapturePolicyRequest
	err = decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	policyType, ok := parsePolicyType(w, req.Type)
	if !ok {
		return
	}

	err = s.ing.SetPolicy(channel, policyType, ingest.PolicyParams{
		Prerecord:  req.Params.PrerecordNS,
		Postrecord: req.Params.PostrecordNS,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type triggerEventRequest struct {
	T *uint64 `json:"t,omitempty"`
}

func (s *HttpApiServer) handleTriggerEvent(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req triggerEventRequest
	if r.ContentLength != 0 {
		err := decodeJSON(r, &req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	now := uint64(time.Now().UnixNano())
	eventTime := now
	if req.T != nil {
		eventTime = *req.T
	}
	err = s.ing.TriggerEvent(channel, now, eventTime)
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, ingest.ErrWrongPolicyType) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	if s.push != nil {
		s.push.Publish(JournalEvent{Name: EventTriggerFired, Channel: channel})
	}
	w.WriteHeader(http.StatusNoContent)
}

type startRecordingRequest struct {
	FromTimeNS *uint64 `json:"from_time_ns,omitempty"`
}

func (s *HttpApiServer) handleStartRecording(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req startRecordingRequest
	if r.ContentLength != 0 {
		err := decodeJSON(r, &req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	now := uint64(time.Now().UnixNano())
	err = s.ing.StartRecording(channel, now, req.FromTimeNS)
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, ingest.ErrWrongPolicyType) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	if s.push != nil {
		s.push.Publish(JournalEvent{Name: EventRecordingCommandStart, Channel: channel})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *HttpApiServer) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	now := uint64(time.Now().UnixNano())
	err = s.ing.StopRecording(channel, now)
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, ingest.ErrWrongPolicyType) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	if s.push != nil {
		s.push.Publish(JournalEvent{Name: EventRecordingCommandStop, Channel: channel})
	}
	w.WriteHeader(http.StatusNoContent)
}

// findChannel returns the running ChannelInfo for channel, if any -- used by
// archives.go's config_update, which (unlike PUT /channels/{id}) only
// carries pre_record/post_record and needs the channel's current
// PolicyType to call SetPolicy without disturbing it.
func (s *HttpApiServer) findChannel(channel uint16) (ingest.ChannelInfo, bool) {
	for _, c := range s.ing.List() {
		if c.Channel == channel {
			return c, true
		}
	}
	return ingest.ChannelInfo{}, false
}

// channelInfo is GET /channels' listing shape -- a plain projection of
// ingest.ChannelInfo with the wire's usual snake_case/string conventions
// (PolicyType as its String() form, not the bare int ingest.PolicyType is
// internally; PolicyParams flattened rather than nested, matching
// setCapturePolicyRequest's own params shape).
type channelInfo struct {
	Channel      uint16 `json:"channel"`
	RTSPURL      string `json:"rtsp_url"`
	Storage      string `json:"storage"`
	PolicyType   string `json:"capture_policy_type"`
	PrerecordNS  uint64 `json:"prerecord_ns"`
	PostrecordNS uint64 `json:"postrecord_ns"`
	Name         string `json:"name,omitempty"`
}

func (s *HttpApiServer) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeJSON(w, http.StatusOK, []channelInfo{})
		return
	}
	list := s.ing.List()
	out := make([]channelInfo, len(list))
	for i, c := range list {
		out[i] = channelInfo{
			Channel: c.Channel, RTSPURL: c.RTSPURL, Storage: c.StorageID,
			PolicyType: c.PolicyType.String(), PrerecordNS: c.PolicyParams.Prerecord, PostrecordNS: c.PolicyParams.Postrecord,
			Name: c.Name,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// channelCapturePolicyRequest is create/update channel's nested
// capture_policy object. MaxDeferredStartNS sizes the frame queue for a
// continuous channel (ingest.ChannelConfig.QueueDepth); PrerecordNS does
// the same job for an event channel -- see internal/farcd.buildChannelConfig,
// which queueDepthFor below mirrors exactly (duplicated rather than
// imported: internal/api deliberately doesn't depend on internal/farcd or
// internal/config, matching this file's other handlers' own wire structs).
type channelCapturePolicyRequest struct {
	Type               string `json:"type"`
	MaxDeferredStartNS uint64 `json:"max_deferred_start_ns,omitempty"`
	PrerecordNS        uint64 `json:"prerecord_ns,omitempty"`
	PostrecordNS       uint64 `json:"postrecord_ns,omitempty"`
}

func queueDepthFor(policyType ingest.PolicyType, req channelCapturePolicyRequest) uint64 {
	if policyType == ingest.PolicyEvent {
		return req.PrerecordNS
	}
	return req.MaxDeferredStartNS
}

// ChannelSpec is the config-file-relevant subset of a channel's fields,
// passed to the OnChannel* hooks below -- internal/farcd persists it into
// its own config file (mirroring SetOnStorageCreated's role for storages,
// PLAN.md's Gap 3). PolicyType is the config file's own string ("continuous"/
// "event"), not ingest.PolicyType, so farcd never needs to round-trip
// through the enum to write it back out.
type ChannelSpec struct {
	ID                 uint16
	RTSPURL            string
	Storage            string
	PolicyType         string
	MaxDeferredStartNS uint64
	PrerecordNS        uint64
	PostrecordNS       uint64
	Name               string
}

func specFromRequest(id uint16, rtspURL, storage string, cp channelCapturePolicyRequest, name string) ChannelSpec {
	return ChannelSpec{
		ID: id, RTSPURL: rtspURL, Storage: storage, PolicyType: cp.Type,
		MaxDeferredStartNS: cp.MaxDeferredStartNS, PrerecordNS: cp.PrerecordNS, PostrecordNS: cp.PostrecordNS,
		Name: name,
	}
}

type createChannelRequest struct {
	ID            uint16                      `json:"id"`
	RTSPURL       string                      `json:"rtsp_url"`
	Storage       string                      `json:"storage"`
	CapturePolicy channelCapturePolicyRequest `json:"capture_policy"`
	Name          string                      `json:"name,omitempty"`
}

// createChannel is handleCreateChannel's HTTP-free core, shared with
// archives.go's channels_add (which creates several channels per request,
// each needing the same validation/AddChannel/persist sequence and the same
// per-item error handling this returns via *apiError).
func (s *HttpApiServer) createChannel(req createChannelRequest) (channelInfo, error) {
	if req.ID == 0 {
		return channelInfo{}, apiErr(http.StatusBadRequest, errChannelIDReserved)
	}
	if req.RTSPURL == "" {
		return channelInfo{}, apiErr(http.StatusBadRequest, errRTSPURLRequired)
	}
	unit, ok := s.reg.Get(req.Storage)
	if !ok {
		return channelInfo{}, apiErr(http.StatusBadRequest, fmt.Errorf("api: unknown storage %q", req.Storage))
	}
	policyType, ok := parsePolicyTypeString(req.CapturePolicy.Type)
	if !ok {
		return channelInfo{}, policyTypeErr(req.CapturePolicy.Type)
	}

	cfg := ingest.ChannelConfig{
		Channel:    req.ID,
		RTSPURL:    req.RTSPURL,
		StorageID:  req.Storage,
		Recorder:   unit,
		QueueDepth: queueDepthFor(policyType, req.CapturePolicy),
		PolicyType: policyType,
		PolicyParams: ingest.PolicyParams{
			Prerecord: req.CapturePolicy.PrerecordNS, Postrecord: req.CapturePolicy.PostrecordNS,
		},
		ReadTimeout:        channelTimeout,
		WriteTimeout:       channelTimeout,
		BackpressureSignal: func() bool { return unit.EngineLevel() == storageengine.LevelBackpressure },
		Name:               req.Name,
	}
	err := s.ing.AddChannel(cfg)
	if err != nil {
		return channelInfo{}, apiErr(http.StatusConflict, err)
	}

	spec := specFromRequest(req.ID, req.RTSPURL, req.Storage, req.CapturePolicy, req.Name)
	err = s.onChannelCreated(spec)
	if err != nil {
		_, _ = s.ing.RemoveChannel(req.ID)
		return channelInfo{}, fmt.Errorf("api: persist channel %d: %w", req.ID, err)
	}
	return channelInfo{
		Channel: req.ID, RTSPURL: req.RTSPURL, Storage: req.Storage,
		PolicyType: policyType.String(), PrerecordNS: req.CapturePolicy.PrerecordNS, PostrecordNS: req.CapturePolicy.PostrecordNS,
		Name: req.Name,
	}, nil
}

func (s *HttpApiServer) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	var req createChannelRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	info, err := s.createChannel(req)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

type updateChannelRequest struct {
	RTSPURL       string                      `json:"rtsp_url"`
	Storage       string                      `json:"storage"`
	CapturePolicy channelCapturePolicyRequest `json:"capture_policy"`
	Name          string                      `json:"name,omitempty"`
}

// handleUpdateChannel replaces an already-running channel's rtsp_url/
// storage/capture-policy wholesale. There's no cheap in-place path for
// rtsp_url/storage (ChannelIngest.Run takes rtspURL as a fixed parameter,
// and switching storages means a different Recorder) -- this is genuinely
// remove-then-add under the hood, with the removed config restored if any
// later step fails, so a request that ends in an error never leaves the
// channel stopped.
func (s *HttpApiServer) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req updateChannelRequest
	err = decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RTSPURL == "" {
		writeError(w, http.StatusBadRequest, errRTSPURLRequired)
		return
	}
	unit, ok := s.reg.Get(req.Storage)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: unknown storage %q", req.Storage))
		return
	}
	policyType, ok := parsePolicyType(w, req.CapturePolicy.Type)
	if !ok {
		return
	}

	old, err := s.ing.RemoveChannel(channel)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	cfg := ingest.ChannelConfig{
		Channel:    channel,
		RTSPURL:    req.RTSPURL,
		StorageID:  req.Storage,
		Recorder:   unit,
		QueueDepth: queueDepthFor(policyType, req.CapturePolicy),
		PolicyType: policyType,
		PolicyParams: ingest.PolicyParams{
			Prerecord: req.CapturePolicy.PrerecordNS, Postrecord: req.CapturePolicy.PostrecordNS,
		},
		ReadTimeout:        channelTimeout,
		WriteTimeout:       channelTimeout,
		BackpressureSignal: func() bool { return unit.EngineLevel() == storageengine.LevelBackpressure },
		Name:               req.Name,
	}
	err = s.ing.AddChannel(cfg)
	if err != nil {
		// Only plausible if another request raced to (re-)create the same
		// id between our Remove and Add above -- restore what we had.
		_ = s.ing.AddChannel(old)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	spec := specFromRequest(channel, req.RTSPURL, req.Storage, req.CapturePolicy, req.Name)
	err = s.onChannelUpdated(spec)
	if err != nil {
		_, _ = s.ing.RemoveChannel(channel)
		_ = s.ing.AddChannel(old)
		writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist channel %d: %w", channel, err))
		return
	}
	writeJSON(w, http.StatusOK, channelInfo{
		Channel: channel, RTSPURL: req.RTSPURL, Storage: req.Storage,
		PolicyType: policyType.String(), PrerecordNS: req.CapturePolicy.PrerecordNS, PostrecordNS: req.CapturePolicy.PostrecordNS,
		Name: req.Name,
	})
}

// removeChannel is handleRemoveChannel's HTTP-free core, shared with
// archives.go's channels_del (which removes several channels per request).
func (s *HttpApiServer) removeChannel(channel uint16) error {
	old, err := s.ing.RemoveChannel(channel)
	if err != nil {
		return apiErr(http.StatusNotFound, err)
	}
	err = s.onChannelRemoved(channel)
	if err != nil {
		_ = s.ing.AddChannel(old)
		return fmt.Errorf("api: persist removal of channel %d: %w", channel, err)
	}
	return nil
}

func (s *HttpApiServer) handleRemoveChannel(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, errNoIngestManager)
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err = s.removeChannel(channel)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
