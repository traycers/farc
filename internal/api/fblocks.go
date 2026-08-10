package api

import (
	"encoding/hex"
	"fmt"
	"net/http"

	"traycers/farc/fblock"
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

// handleListFblocks implements GET /storages/{id}/fblocks -- the
// fblock-status page's table of every fblock in a storage, so a user can
// browse and pick one without already knowing its uuid. Reads
// unit.Index().Snapshot() (the same *fblock.Catalog handleCandidates already
// uses for Begin/End/UUID) directly, with no channel filter at all.
func (s *HttpApiServer) handleListFblocks(w http.ResponseWriter, r *http.Request) {
	unit, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", r.PathValue("id")))
		return
	}

	snap := unit.Index().Snapshot()
	out := make([]fblockInfo, snap.N)
	for i := uint32(0); i < snap.N; i++ {
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
		out[i] = info
	}
	writeJSON(w, http.StatusOK, out)
}
