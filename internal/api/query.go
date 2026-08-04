package api

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

func parseQueryChannelTimeRange(r *http.Request) (channel uint16, t1, t2 uint64, err error) {
	q := r.URL.Query()
	ch, err := strconv.ParseUint(q.Get("channel"), 10, 16)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("api: invalid channel: %w", err)
	}
	t1, err = strconv.ParseUint(q.Get("t1"), 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("api: invalid t1: %w", err)
	}
	t2, err = strconv.ParseUint(q.Get("t2"), 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("api: invalid t2: %w", err)
	}
	return uint16(ch), t1, t2, nil
}

// candidateInfo is one GET .../candidates result entry — enough for a
// caller to fetch this fcontainer's TOC (GET .../toc) and confirm the exact
// match itself, per ADR-014's "маска сокращает число кандидатов, а не
// заменяет TOC".
type candidateInfo struct {
	Index uint32 `json:"index"`
	UUID  string `json:"uuid"`
	Begin uint64 `json:"begin"`
	End   uint64 `json:"end"`
}

// handleCandidates implements GET .../candidates?channel=&t1=&t2=
// (ADR-014): fblock-level candidates only, no TOC read.
func (s *HttpApiServer) handleCandidates(w http.ResponseWriter, r *http.Request) {
	unit, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", r.PathValue("id")))
		return
	}
	channel, t1, t2, err := parseQueryChannelTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	indices := unit.Candidates(channel, t1, t2)
	snap := unit.Index().Snapshot()
	out := make([]candidateInfo, len(indices))
	for i, idx := range indices {
		out[i] = candidateInfo{
			Index: idx,
			UUID:  hex.EncodeToString(snap.UUID[idx][:]),
			Begin: snap.Begin[idx],
			End:   snap.End[idx],
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// resolvedFrame is one GET .../resolve result entry (ADR-016 fallback):
// self-describing, since the whole point of this endpoint is serving a
// consumer with no parsed TOC of its own to interpret raw offsets against.
type resolvedFrame struct {
	UUID string `json:"uuid"`
	Time uint64 `json:"time"`
	Kind *uint8 `json:"kind,omitempty"` // video only (mediatree.FrameKind*)
	Data string `json:"data"`           // base64
}

// handleResolve implements GET .../resolve?channel=&t1=&t2= (ADR-016):
// finds every candidate fblock (ADR-014), reads and confirms each one's TOC
// itself, and returns the matching frames' actual data — the fallback path
// for a consumer that doesn't have a parsed TOC on hand (e.g. first request
// after (re)connecting). No width limit is enforced on [t1,t2] — ADR-016
// itself leaves that as an open question, not a decided bound.
func (s *HttpApiServer) handleResolve(w http.ResponseWriter, r *http.Request) {
	unit, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", r.PathValue("id")))
		return
	}
	channel, t1, t2, err := parseQueryChannelTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	indices := unit.Candidates(channel, t1, t2)
	snap := unit.Index().Snapshot()

	var out []resolvedFrame
	for _, idx := range indices {
		uuid := snap.UUID[idx]
		columns, err := unit.ReadTOC(uuid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("api: resolve: read toc for candidate %x: %w", uuid, err))
			return
		}
		frames, err := resolveChannelFrames(unit, uuid, columns, channel, t1, t2)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, frames...)
	}
	writeJSON(w, http.StatusOK, out)
}

// resolveChannelFrames implements the fallback resolve algorithm itself
// (docs/docs/archive/adr/016-toc-resolve-fallback.md's solution paragraph):
// find the channel's node, take its subtree range, scan frame_time nodes
// within it, filter by [t1,t2], then read each matched frame's data.
func resolveChannelFrames(unit *storage.Unit, uuid [16]byte, c *toc.Columns, channel uint16, t1, t2 uint64) ([]resolvedFrame, error) {
	channelNodeID, ok := findChannelNode(c, channel)
	if !ok {
		return nil, nil // mask false positive (ADR-014) or channel not present in this fcontainer
	}
	start, end := toc.SubtreeRange(c, channelNodeID)

	timeIDs := toc.ScanByRole(c, mediatree.RoleFrameTimeVideo, mediatree.RoleFrameTimeAudio)
	timeIDs = toc.InRange(timeIDs, start, end)
	timeIDs = toc.TimeRange(c, timeIDs, t1, t2)

	out := make([]resolvedFrame, 0, len(timeIDs))
	for _, timeID := range timeIDs {
		frameID := c.Parent[timeID]
		dataRole, kindRole := mediatree.RoleFrameDataVideo, mediatree.Role(0)
		hasKind := false
		if c.Role[frameID] == mediatree.RoleFrameAudio {
			dataRole = mediatree.RoleFrameDataAudio
		} else {
			kindRole = mediatree.RoleFrameKind
			hasKind = true
		}

		dataID, ok := findChildByRole(c, frameID, dataRole)
		if !ok {
			return nil, fmt.Errorf("api: resolve: frame %d has no data child", frameID)
		}
		offset, size, ok := toc.ContentOffset(c, dataID)
		if !ok {
			return nil, fmt.Errorf("api: resolve: frame %d data node is not variable-width", frameID)
		}
		data, err := unit.ReadRange(uuid, offset, size)
		if err != nil {
			return nil, fmt.Errorf("api: resolve: read frame %d data: %w", frameID, err)
		}

		frame := resolvedFrame{
			UUID: hex.EncodeToString(uuid[:]),
			Time: c.ValueOrOffset[timeID],
			Data: base64.StdEncoding.EncodeToString(data),
		}
		if hasKind {
			if kindID, ok := findChildByRole(c, frameID, kindRole); ok {
				if v, ok := toc.InlineValue(c, kindID); ok && len(v) == 1 {
					frame.Kind = &v[0]
				}
			}
		}
		out = append(out, frame)
	}
	return out, nil
}

// findChannelNode finds the RoleChannel node whose inline uint32 value is
// channel, within c's own row-index space.
func findChannelNode(c *toc.Columns, channel uint16) (uint32, bool) {
	for _, id := range toc.ScanByRole(c, mediatree.RoleChannel) {
		v, ok := toc.InlineValue(c, id)
		if ok && len(v) == 4 && binary.LittleEndian.Uint32(v) == uint32(channel) {
			return id, true
		}
	}
	return 0, false
}

// findChildByRole scans parentID's own subtree range for a direct child
// (parent == parentID) with the given role — toc.Columns has no ready-made
// equivalent of mediatree.FindChildByRole (that helper works on []Element,
// the pre-reorder write-time representation, not post-reorder Columns).
func findChildByRole(c *toc.Columns, parentID uint32, role mediatree.Role) (uint32, bool) {
	_, end := toc.SubtreeRange(c, parentID)
	for i := parentID + 1; i < end; i++ {
		if c.Parent[i] == parentID && c.Role[i] == role {
			return i, true
		}
	}
	return 0, false
}
