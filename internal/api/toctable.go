package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// fblockLiveSig is the live-WS poll loop's change-detection signature --
// cheap to compare every tick without diffing message content. Moved here
// from fblocktree.go (issue 04) when handleFblockLiveTreeWS, its only
// other user, was deleted -- this file's handleFblockLiveTOCRowsWS is now
// the sole live-WS handler left.
type fblockLiveSig struct {
	generation uint64
	elemCount  int
}

var liveTreeUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

const liveTreePollInterval = 500 * time.Millisecond

// tocRow is one node's flat, SoA-shaped view of a fblock's TOC (or, for a
// currently in_progress fblock, its live in-memory equivalent) -- the
// fblock-toc-table page's data shape, deliberately not the nested TreeNode
// tree (settled via grilling, 2026-08-21: TOC already is a
// Structure-of-Arrays on disk, so this projects it straight into rows
// instead of re-flattening a tree built for a different consumer).
// ParentID/SiblingID follow TreeNode's naming convention (parent_id), not
// toc.Columns' own field names, so both row-shaped APIs read consistently.
// ValueOrOffset is always a raw number, never a resolved string/bytes
// value: for fixed-width types it's the packed inline value, for
// bytes/string types it's the raw Content byte offset -- the same
// structure-only promise TreeNode already makes for variable-width nodes.
type tocRow struct {
	ID        uint32 `json:"id"`
	Type      string `json:"type"`
	Role      string `json:"role"`
	ParentID  uint32 `json:"parent_id"`
	SiblingID uint32 `json:"sibling_id"`
	// ValueOrOffset is decimal-string encoded (`,string`), not a bare JSON
	// number: a timestamp/duration node's packed inline value is a unix-ns
	// uint64 (~1.7e18), past JS's 2^53 safe-integer limit -- the same
	// reason TreeNode.Value is already a string, not a number.
	ValueOrOffset uint64 `json:"value_or_offset,string"`
	Size          uint64 `json:"size"`
}

// tocRowsFromColumns projects a finished fblock's decoded TOC directly into
// rows, in the same post-DFS-reorder row order the TOC already stores on
// disk -- no tree-building walk (toc.Children/toc.SubtreeRange) needed,
// unlike buildColumnsTree.
func tocRowsFromColumns(c *toc.Columns) []tocRow {
	rows := make([]tocRow, c.N)
	for i := uint32(0); i < c.N; i++ {
		rows[i] = tocRow{
			ID:            i,
			Type:          c.Type[i].String(),
			Role:          c.Role[i].String(),
			ParentID:      c.Parent[i],
			SiblingID:     c.Sibling[i],
			ValueOrOffset: c.ValueOrOffset[i],
			Size:          c.Size[i],
		}
	}
	return rows
}

// handleReadTOCRows implements GET .../fcontainers/{uuid}/toc/rows: the
// fblock-toc-table page's data source for a finished (ready) fblock.
func (s *HttpApiServer) handleReadTOCRows(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, tocRowsFromColumns(columns))
}

// liveValueOrOffset computes tocRow.ValueOrOffset for a live (in_progress)
// element. For variable-width (bytes/string) types there is no Content
// byte offset yet -- Content for an in_progress fblock is still being
// appended and no TOC has been built -- so this returns 0, matching
// today's live tree, which likewise never exposes a byte offset for these
// nodes (fblocktree.go's liveNode only ever sets Size for them). For
// fixed-width types, packs e.Value the same way toc.Build does via
// toc.PackInline, so a live row and its eventual ready row carry the
// bit-identical numeric value for the same logical field.
func liveValueOrOffset(e mediatree.Element) uint64 {
	fixedSize, ok := e.Type.FixedSize()
	if !ok {
		return 0
	}
	return toc.PackInline(e.Value, fixedSize)
}

// liveSize computes tocRow.Size for a live element -- 0 for fixed-width
// types (matching toc.Columns.Size, which toc.Build always sets to 0 for
// them), len(e.Value) only for variable-width types. Getting this wrong
// (e.g. always len(e.Value)) would make the same logical fixed-width node
// report a nonzero size live and 0 once ready, breaking the "same row
// shape regardless of state" promise.
func liveSize(e mediatree.Element) uint64 {
	if e.Type.Variable() {
		return uint64(len(e.Value))
	}
	return 0
}

// tocRowsFromElements projects a currently in_progress fblock's live
// in-memory elements (creation order) into the same row shape
// tocRowsFromColumns produces for a finished fblock. No DFS reorder is
// needed here -- elems is already in creation order with Parent[i] <= i
// guaranteed (same invariant buildLiveTree relies on), unlike the ready
// path, where toc.Build's reorder is what produces the row ids in the
// first place.
func tocRowsFromElements(elems []mediatree.Element) []tocRow {
	rows := make([]tocRow, len(elems))
	for i, e := range elems {
		rows[i] = tocRow{
			ID:            uint32(i),
			Type:          e.Type.String(),
			Role:          e.Role.String(),
			ParentID:      e.Parent,
			SiblingID:     e.Sibling,
			ValueOrOffset: liveValueOrOffset(e),
			Size:          liveSize(e),
		}
	}
	return rows
}

// tocRowsLiveMessage is the fblock-toc-table page's live-data WS frame
// shape for an in_progress fblock -- Rows is nil/omitted if nothing has
// been observed yet for this Storage's shared segment, same convention as
// fblockLiveTreeMessage.
type tocRowsLiveMessage struct {
	Rows []tocRow `json:"rows,omitempty"`
}

// buildFblockLiveTOCRowsMessage is buildFblockLiveTreeMessage's row-shaped
// counterpart, sharing the same fblockLiveSig change-detection type since
// both poll the exact same LiveTreeForStorage snapshot.
func (s *HttpApiServer) buildFblockLiveTOCRowsMessage(storageID string) (tocRowsLiveMessage, fblockLiveSig) {
	elems, gen, ok := s.ing.LiveTreeForStorage(storageID)
	if !ok {
		return tocRowsLiveMessage{}, fblockLiveSig{}
	}
	return tocRowsLiveMessage{Rows: tocRowsFromElements(elems)}, fblockLiveSig{generation: gen, elemCount: len(elems)}
}

// handleFblockLiveTOCRowsWS implements GET
// /storages/{id}/fblocks/{index}/toc/rows/ws: the fblock-toc-table page's
// live-data source for a currently-writing fblock -- the row-shaped
// counterpart of handleFblockLiveTreeWS, same gating/poll/resend structure.
func (s *HttpApiServer) handleFblockLiveTOCRowsWS(w http.ResponseWriter, r *http.Request) {
	unit, id, ok := s.resolveUnit(w, r)
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
	if snap.State(idx) != fblock.InProgress {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: fblock %d is not live (state=%s)", idx, snap.State(idx)))
		return
	}

	conn, err := liveTreeUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the HTTP error response
	}
	defer func() { _ = conn.Close() }()

	msg, lastSig := s.buildFblockLiveTOCRowsMessage(id)
	if conn.WriteJSON(msg) != nil {
		return
	}

	closed := watchDisconnect(conn)
	ticker := time.NewTicker(liveTreePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-closed:
			return
		case <-ticker.C:
			msg, sig := s.buildFblockLiveTOCRowsMessage(id)
			if sig == lastSig {
				continue
			}
			lastSig = sig
			if conn.WriteJSON(msg) != nil {
				return
			}
		}
	}
}
