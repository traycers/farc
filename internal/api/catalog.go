package api

import (
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/traycers/farc/fblock"
)

// catalogEntry is one fblock's row in GET /storages/{id}/catalog's bulk
// response -- Manager.Snapshot()'s Structure-of-Arrays reshaped into the
// per-fblock view a diff-based bootstrap
// (.scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md)
// needs, mirroring fblockInfo's field set/JSON conventions (hex UUID,
// decimal-string Begin/End for 64-bit JS safety, UUID/Begin/End only
// meaningful and only populated for a Ready fblock).
type catalogEntry struct {
	Index     uint32 `json:"index"`
	State     string `json:"state"`
	UUID      string `json:"uuid,omitempty"`
	Begin     string `json:"begin,omitempty"`
	End       string `json:"end,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

// handleGetCatalog implements GET /storages/{id}/catalog: every fblock's
// state in one response, so a caller like hlsd's diff-based bootstrap
// doesn't need N per-index requests (GET .../fblocks/{index}) just to learn
// which uuids are still live. Deliberately unfiltered by channel (decided
// 2026-08-13) -- the catalog's channel bitmap could narrow this per-caller,
// but the cache/diff logic this feeds operates at (storage, uuid) grain
// regardless, so filtering is a payload-size optimization with no current
// need.
func (s *HttpApiServer) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	unit, _, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	snap := unit.Index().Snapshot()
	entries := make([]catalogEntry, snap.N)
	for i := range entries {
		idx := uint32(i)
		entry := catalogEntry{Index: idx, State: snap.State(idx).String()}
		if snap.State(idx) == fblock.Ready {
			entry.UUID = hex.EncodeToString(snap.UUID[idx][:])
			entry.Begin = strconv.FormatUint(snap.Begin[idx], 10)
			entry.End = strconv.FormatUint(snap.End[idx], 10)
			entry.Protected = snap.Protected(idx)
		}
		entries[i] = entry
	}
	writeJSON(w, http.StatusOK, entries)
}
