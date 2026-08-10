package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"traycers/farc/fblock"
)

// defaultFblocksLimit/maxFblocksLimit bound GET .../fblocks paging -- a
// storage's physical fblock count scales with disk size (docs/docs/archive's
// requirements call for 20+ TB storages), so returning the whole catalog
// unpaginated on every request doesn't scale; 100 matches the fblock-status
// page's list view, 1000 is a generous ceiling against accidental abuse.
const (
	defaultFblocksLimit = 100
	maxFblocksLimit     = 1000
)

// fblockInfo is one GET .../fblocks result entry -- a whole-storage,
// channel-agnostic listing (unlike candidateInfo/handleCandidates, which
// require picking a channel+time range up front). UUID/Begin/End/Channels
// are only meaningful once the fblock is Ready; they're simply omitted
// otherwise (an in-progress/uninitialized/bad fblock has no valid content
// to report).
type fblockInfo struct {
	Index     uint32   `json:"index"`
	State     string   `json:"state"`
	UUID      string   `json:"uuid,omitempty"`
	Begin     uint64   `json:"begin,omitempty"`
	End       uint64   `json:"end,omitempty"`
	Protected bool     `json:"protected"`
	Channels  []uint16 `json:"channels,omitempty"`
}

// fblockListResponse is GET .../fblocks's response envelope. Total is the
// storage's full physical fblock count (independent of offset/limit), so a
// caller can page through it (e.g. Total/limit pages of 100).
type fblockListResponse struct {
	Total   int          `json:"total"`
	Fblocks []fblockInfo `json:"fblocks"`
}

// parseFblocksPaging reads ?offset=&limit= (both optional), clamping limit
// to (0, maxFblocksLimit].
func parseFblocksPaging(r *http.Request) (offset, limit int, err error) {
	q := r.URL.Query()
	offset = 0
	if s := q.Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("api: invalid offset %q", s)
		}
	}
	limit = defaultFblocksLimit
	if s := q.Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit <= 0 {
			return 0, 0, fmt.Errorf("api: invalid limit %q", s)
		}
		if limit > maxFblocksLimit {
			limit = maxFblocksLimit
		}
	}
	return offset, limit, nil
}

// handleListFblocks implements GET /storages/{id}/fblocks?offset=&limit= --
// the fblock-status page's table of every fblock in a storage, so a user can
// browse and pick one without already knowing its uuid. Reads
// unit.Index().Snapshot() (the same *fblock.Catalog handleCandidates already
// uses for Begin/End/UUID) directly, with no channel filter at all.
func (s *HttpApiServer) handleListFblocks(w http.ResponseWriter, r *http.Request) {
	unit, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", r.PathValue("id")))
		return
	}
	offset, limit, err := parseFblocksPaging(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	snap := unit.Index().Snapshot()
	total := int(snap.N)
	start := min(offset, total)
	end := min(start+limit, total)

	out := make([]fblockInfo, 0, end-start)
	for i := uint32(start); i < uint32(end); i++ {
		info := fblockInfo{Index: i, State: snap.State(i).String(), Protected: snap.Protected(i)}
		if snap.State(i) == fblock.Ready {
			info.UUID = hex.EncodeToString(snap.UUID[i][:])
			info.Begin = snap.Begin[i]
			info.End = snap.End[i]
			for pos, ch := range snap.ChannelRegistry {
				if ch != 0 && snap.ChannelBit(i, uint16(pos)) {
					info.Channels = append(info.Channels, ch)
				}
			}
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, fblockListResponse{Total: total, Fblocks: out})
}
