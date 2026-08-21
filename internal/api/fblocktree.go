package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"github.com/traycers/farc/fblock"
)

// fblockInfo is GET .../fblocks/{index}'s response: enough for a client to
// resolve a physical index to the UUID handleReadTOCRows needs, plus the
// catalog metadata fblock-status/fblock-toc-table display alongside the
// tree/table. Fields beyond Index/State are only meaningful (and only
// populated) for a Ready fblock.
type fblockInfo struct {
	Index     uint32 `json:"index"`
	State     string `json:"state"`
	UUID      string `json:"uuid,omitempty"`
	Begin     string `json:"begin,omitempty"` // decimal ns, string (64-bit safety)
	End       string `json:"end,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

// handleGetFblock implements GET /storages/{id}/fblocks/{index} -- no
// existing endpoint resolves a bare physical index to its UUID/metadata
// (Candidates requires a channel+time range), which fblock-status and
// fblock-toc-table both need to go from "index in the URL" to a GET
// .../fcontainers/{uuid}/toc/rows call.
func (s *HttpApiServer) handleGetFblock(w http.ResponseWriter, r *http.Request) {
	unit, _, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	idx64, err := strconv.ParseUint(r.PathValue("index"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid fblock index %q: %w", r.PathValue("index"), err))
		return
	}
	idx := uint32(idx64)
	snap := unit.Index().Snapshot()
	if idx >= snap.N {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: fblock index %d out of range (N=%d)", idx, snap.N))
		return
	}
	info := fblockInfo{Index: idx, State: snap.State(idx).String()}
	if snap.State(idx) == fblock.Ready {
		info.UUID = hex.EncodeToString(snap.UUID[idx][:])
		info.Begin = strconv.FormatUint(snap.Begin[idx], 10)
		info.End = strconv.FormatUint(snap.End[idx], 10)
		info.Protected = snap.Protected(idx)
	}
	writeJSON(w, http.StatusOK, info)
}
