package api

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// TreeNode is the shared JSON node shape for fblock-status's full-tree GET
// response and fblock-live's WS "snapshot"/"append" node objects. A full
// nested tree (fblock-status, and fblock-live's "snapshot"/"reset") always
// populates Children; fblock-live's "append" sends a flat slice of newly
// created nodes with Children left nil -- their own children (if any)
// haven't been created yet and arrive later as their own entries in this
// or a subsequent "append" batch. ID/ParentID let the client attach each
// flat node into its own tree model itself. Root is ID==ParentID==0, the
// same self-reference convention as mediatree.Element/toc.Columns.
//
// This is a structure view, not a content view: variable-width (string/
// bytes) nodes report only Size, never their actual payload bytes -- no
// Content-section read happens to build this response at all.
type TreeNode struct {
	ID       uint32      `json:"id"`
	ParentID uint32      `json:"parent_id"`
	Type     string      `json:"type"`            // mediatree.NodeType.String()
	Role     string      `json:"role"`            // mediatree.Role.String()
	Value    string      `json:"value,omitempty"` // fixed-width types only
	Size     *uint64     `json:"size,omitempty"`  // string/bytes only: byte length
	Children []*TreeNode `json:"children,omitempty"`
}

// formatNodeValue decodes raw (a fixed-width node's inline value bytes, per
// mediatree.NodeType.FixedSize) into the decimal/text form TreeNode.Value
// sends over the wire. Always a string -- avoids a string|number union on
// the TS side and sidesteps uint64/int64 exceeding JS's 53-bit safe integer
// range. Timestamp/Duration are sent as raw unsigned ns, unformatted: this
// package has no business deciding display formatting, that's the client's
// job (mirroring the existing api/ns.ts convention for other endpoints).
// Void/String/Bytes/malformed input all return "".
func formatNodeValue(t mediatree.NodeType, raw []byte) string {
	switch t {
	case mediatree.TypeUint8:
		if len(raw) < 1 {
			return ""
		}
		return strconv.FormatUint(uint64(raw[0]), 10)
	case mediatree.TypeUint32:
		if len(raw) < 4 {
			return ""
		}
		return strconv.FormatUint(uint64(binary.LittleEndian.Uint32(raw)), 10)
	case mediatree.TypeUint64, mediatree.TypeTimestamp, mediatree.TypeDuration:
		if len(raw) < 8 {
			return ""
		}
		return strconv.FormatUint(binary.LittleEndian.Uint64(raw), 10)
	case mediatree.TypeInt32:
		if len(raw) < 4 {
			return ""
		}
		return strconv.FormatInt(int64(int32(binary.LittleEndian.Uint32(raw))), 10)
	case mediatree.TypeInt64:
		if len(raw) < 8 {
			return ""
		}
		return strconv.FormatInt(int64(binary.LittleEndian.Uint64(raw)), 10)
	case mediatree.TypeFloat32:
		if len(raw) < 4 {
			return ""
		}
		return strconv.FormatFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(raw))), 'g', -1, 32)
	case mediatree.TypeFloat64:
		if len(raw) < 8 {
			return ""
		}
		return strconv.FormatFloat(math.Float64frombits(binary.LittleEndian.Uint64(raw)), 'g', -1, 64)
	case mediatree.TypeVoid, mediatree.TypeString, mediatree.TypeBytes:
		return ""
	default: // anything unrecognized
		return ""
	}
}

// buildColumnsTree walks a decoded TOC into the shared TreeNode shape,
// starting at the root (position 0). Returns nil for an empty TOC.
func buildColumnsTree(c *toc.Columns) *TreeNode {
	if c.N == 0 {
		return nil
	}
	return columnsNode(c, 0)
}

func columnsNode(c *toc.Columns, id uint32) *TreeNode {
	n := &TreeNode{ID: id, ParentID: c.Parent[id], Type: c.Type[id].String(), Role: c.Role[id].String()}
	if c.Type[id].Variable() {
		if _, size, ok := toc.ContentOffset(c, id); ok {
			n.Size = &size
		}
	} else if raw, ok := toc.InlineValue(c, id); ok {
		n.Value = formatNodeValue(c.Type[id], raw)
	}
	for _, cid := range toc.Children(c, id) {
		n.Children = append(n.Children, columnsNode(c, cid))
	}
	return n
}

// handleReadFblockTree implements GET .../fcontainers/{uuid}/tree: the
// structure-only JSON view of a finished fblock's TOC, for fblock-status.
func (s *HttpApiServer) handleReadFblockTree(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, buildColumnsTree(columns))
}

// fblockInfo is GET .../fblocks/{index}'s response: enough for a client to
// resolve a physical index to the UUID handleReadFblockTree needs, plus the
// catalog metadata fblock-status displays alongside the tree. Fields beyond
// Index/State are only meaningful (and only populated) for a Ready fblock.
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
// (Candidates requires a channel+time range), which fblock-status needs to
// go from "index in the URL" to a GET .../fcontainers/{uuid}/tree call.
func (s *HttpApiServer) handleGetFblock(w http.ResponseWriter, r *http.Request) {
	unit, _, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	idx64, err := strconv.ParseUint(mux.Vars(r)["index"], 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid fblock index %q: %w", mux.Vars(r)["index"], err))
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

// liveNode is buildLiveTree/buildLiveNodesFlat's per-element conversion --
// the mediatree.Element (pre-reorder, live in-memory) analogue of
// columnsNode. Unlike toc.Columns' ValueOrOffset union, Element.Value is
// already the raw decoded bytes for every type (no Content-offset
// indirection), so no read beyond the element itself is ever needed.
func liveNode(elems []mediatree.Element, id uint32) *TreeNode {
	e := elems[id]
	n := &TreeNode{ID: id, ParentID: e.Parent, Type: e.Type.String(), Role: e.Role.String()}
	if e.Type.Variable() {
		size := uint64(len(e.Value))
		n.Size = &size
	} else {
		n.Value = formatNodeValue(e.Type, e.Value)
	}
	return n
}

// buildLiveTree nests elems (creation order, Parent[i] < i for i>0) into a
// full TreeNode rooted at id 0. A single parent->children pass, not
// toc.Children-style per-node scans, to stay O(n) even for a segment with
// many frames.
func buildLiveTree(elems []mediatree.Element) *TreeNode {
	if len(elems) == 0 {
		return nil
	}
	kids := make(map[uint32][]uint32, len(elems))
	for i, e := range elems {
		if uint32(i) != e.Parent {
			kids[e.Parent] = append(kids[e.Parent], uint32(i))
		}
	}
	var walk func(uint32) *TreeNode
	walk = func(id uint32) *TreeNode {
		n := liveNode(elems, id)
		for _, cid := range kids[id] {
			n.Children = append(n.Children, walk(cid))
		}
		return n
	}
	return walk(0)
}

// fblockLiveTreeMessage is the fblock-tree page's live-data WS frame shape
// for an in_progress fblock: just the tree, keyed to one physical index
// (unlike the whole-storage view this replaces, this page shows one fblock
// at a time -- see .scratch/fblocks-ui/issues/02). Tree is nil/omitted if
// nothing has been observed yet for this Storage's shared segment (e.g. it
// became in_progress via a path this farcd process's own IngestManager
// hasn't seen any frames through -- a benign, momentary state).
type fblockLiveTreeMessage struct {
	Tree *TreeNode `json:"tree,omitempty"`
}

// fblockLiveSig is handleFblockLiveTreeWS's poll-loop change-detection
// signature -- cheap to compare every tick without diffing tree content.
type fblockLiveSig struct {
	generation uint64
	elemCount  int
}

var liveTreeUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

const liveTreePollInterval = 500 * time.Millisecond

// buildFblockLiveTreeMessage assembles storageID's currently shared
// segment's live tree (nil if IngestManager has no StorageSegment for it
// yet) plus a change-detection signature for handleFblockLiveTreeWS's poll
// loop.
func (s *HttpApiServer) buildFblockLiveTreeMessage(storageID string) (fblockLiveTreeMessage, fblockLiveSig) {
	elems, gen, ok := s.ing.LiveTreeForStorage(storageID)
	if !ok {
		return fblockLiveTreeMessage{}, fblockLiveSig{}
	}
	return fblockLiveTreeMessage{Tree: buildLiveTree(elems)}, fblockLiveSig{generation: gen, elemCount: len(elems)}
}

// handleFblockLiveTreeWS implements GET /storages/{id}/fblocks/{index}/tree/ws:
// the fblock-tree page's live-data source for a currently-writing fblock.
// Only one fblock per Storage can ever be in_progress at a time (Pool's
// FIFO head is the sole occupant with a physical index -- internal/storage/
// pool.go), so "is fblock {index} live right now" is answerable purely from
// the catalog snapshot, with no need to cross-reference Pool/Segment
// internals here at all. Sends a snapshot on connect, then polls every
// liveTreePollInterval and resends only when the signature changed.
func (s *HttpApiServer) handleFblockLiveTreeWS(w http.ResponseWriter, r *http.Request) {
	unit, id, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	idx64, err := strconv.ParseUint(mux.Vars(r)["index"], 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid fblock index %q: %w", mux.Vars(r)["index"], err))
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

	msg, lastSig := s.buildFblockLiveTreeMessage(id)
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
			msg, sig := s.buildFblockLiveTreeMessage(id)
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
