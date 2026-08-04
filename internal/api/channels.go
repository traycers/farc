package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"traycers/farc/internal/ingest"
)

func parseChannelID(r *http.Request) (uint16, error) {
	ch, err := strconv.ParseUint(r.PathValue("id"), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("api: invalid channel id %q: %w", r.PathValue("id"), err)
	}
	return uint16(ch), nil
}

// setCapturePolicyRequest is POST /channels/{id}/capture-policy's body.
// Params fields are ns to match ingest.PolicyParams directly; type
// "schedule" is accepted syntactically but rejected with 501 (see below) —
// PolicySchedule doesn't exist as a value at all (internal/ingest's own
// documented v1 scope), so it can't just be passed through.
type setCapturePolicyRequest struct {
	Type   string `json:"type"`
	Params struct {
		PrerecordNS  uint64 `json:"prerecord_ns"`
		PostrecordNS uint64 `json:"postrecord_ns"`
	} `json:"params"`
}

func (s *HttpApiServer) handleSetCapturePolicy(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("api: no IngestManager wired into this server"))
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req setCapturePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var policyType ingest.PolicyType
	switch req.Type {
	case "continuous":
		policyType = ingest.PolicyContinuous
	case "event":
		policyType = ingest.PolicyEvent
	case "schedule":
		writeError(w, http.StatusNotImplemented, fmt.Errorf("ingest: schedule CapturePolicy is not implemented in v1"))
		return
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: unknown capture-policy type %q", req.Type))
		return
	}

	if err := s.ing.SetPolicy(channel, policyType, ingest.PolicyParams{
		Prerecord:  req.Params.PrerecordNS,
		Postrecord: req.Params.PostrecordNS,
	}); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// triggerEventRequest is POST /channels/{id}/events' body — a plain
// one-shot POST (the sketch's own reasoning: sidesteps the long-idle
// long-poll shape docs/docs/archive/10-capture-policy.md §8 raises as an
// open question, since v1 just fires-and-forgets). T defaults to the
// server's own wall-clock time if omitted.
type triggerEventRequest struct {
	T *uint64 `json:"t,omitempty"`
}

func (s *HttpApiServer) handleTriggerEvent(w http.ResponseWriter, r *http.Request) {
	if s.ing == nil {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("api: no IngestManager wired into this server"))
		return
	}
	channel, err := parseChannelID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req triggerEventRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	now := uint64(time.Now().UnixNano())
	eventTime := now
	if req.T != nil {
		eventTime = *req.T
	}
	if err := s.ing.TriggerEvent(channel, now, eventTime); err != nil {
		status := http.StatusNotFound
		if errors.Is(err, ingest.ErrWrongPolicyType) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
