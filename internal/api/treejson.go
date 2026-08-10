package api

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// defaultTreeChildLimit/maxTreeChildLimit bound how many of a node's
// children handleReadTree returns in one response — a "frames(video)"
// container can have millions of children (docs/docs/archive/
// 08-array-trees.md §3.4), so the fblock-status page pages through them
// rather than ever materializing them all in one JSON body.
const (
	defaultTreeChildLimit = 500
	maxTreeChildLimit     = 5000
)

// treeNodeJSON is one node of a decoded fblock media-tree, shared by the
// finalized-fblock tree endpoint below and (mirrored field-for-field) by
// eventpush.go's live progress push — a byte-holding node (bytes/string)
// reports Size, everything else reports Value (Value/Size are mutually
// exclusive by construction, see decodeScalarValue/byteNodeSize).
type treeNodeJSON struct {
	ID         uint32 `json:"id"`
	Role       string `json:"role"`
	Type       string `json:"type"`
	Parent     uint32 `json:"parent"`
	Value      any    `json:"value,omitempty"`
	Size       uint64 `json:"size,omitempty"`
	ChildCount int    `json:"child_count"`
}

// decodeScalarValue converts typ's raw fixed-width bytes into a JSON-safe
// value. uint64/int64/timestamp/duration are emitted as decimal strings --
// a nanosecond-scale value can exceed JS's 2^53 safe-integer range, the same
// reason web/src/api/ns.ts treats Candidate.begin/end as strings/bigint
// rather than JSON numbers. Returns ok=false for TypeVoid and the
// variable-width types (bytes/string), which carry no inline scalar.
func decodeScalarValue(typ mediatree.NodeType, raw []byte) (value any, ok bool) {
	switch typ {
	case mediatree.TypeVoid, mediatree.TypeString, mediatree.TypeBytes:
		return nil, false
	case mediatree.TypeUint8:
		return uint64(raw[0]), true
	case mediatree.TypeUint32:
		return binary.LittleEndian.Uint32(raw), true
	case mediatree.TypeInt32:
		return int32(binary.LittleEndian.Uint32(raw)), true
	case mediatree.TypeFloat32:
		return math.Float32frombits(binary.LittleEndian.Uint32(raw)), true
	case mediatree.TypeUint64:
		return strconv.FormatUint(binary.LittleEndian.Uint64(raw), 10), true
	case mediatree.TypeInt64:
		return strconv.FormatInt(int64(binary.LittleEndian.Uint64(raw)), 10), true
	case mediatree.TypeFloat64:
		return math.Float64frombits(binary.LittleEndian.Uint64(raw)), true
	case mediatree.TypeTimestamp, mediatree.TypeDuration:
		return strconv.FormatUint(binary.LittleEndian.Uint64(raw), 10), true
	default:
		return nil, false
	}
}

// tocNodeJSON builds id's treeNodeJSON from a decoded TOC, given its already
// -computed child count (see subtreeChildren).
func tocNodeJSON(c *toc.Columns, id uint32, childCount int) treeNodeJSON {
	typ := c.Type[id]
	n := treeNodeJSON{ID: id, Role: c.Role[id].String(), Type: typ.String(), Parent: c.Parent[id], ChildCount: childCount}
	if raw, ok := toc.InlineValue(c, id); ok {
		if v, ok := decodeScalarValue(typ, raw); ok {
			n.Value = v
		}
	} else if _, size, ok := toc.ContentOffset(c, id); ok {
		n.Size = size
	}
	return n
}

// subtreeChildren returns k's immediate children, in order, together with a
// direct-child count for every position in k's subtree (indexed as
// childCount[id-k]) — computed in one linear pass over the subtree range
// (docs/docs/archive/06-toc-format.md §6's SubtreeRange), since the TOC's
// array-tree columns store no child-count of their own.
func subtreeChildren(c *toc.Columns, k uint32) (children []uint32, childCount []int) {
	_, end := toc.SubtreeRange(c, k)
	childCount = make([]int, end-k)
	for i := k + 1; i < end; i++ {
		parent := c.Parent[i]
		childCount[parent-k]++
		if parent == k {
			children = append(children, i)
		}
	}
	return children, childCount
}

type treeLevelResponse struct {
	Node     treeNodeJSON   `json:"node"`
	Children []treeNodeJSON `json:"children"`
	Offset   int            `json:"offset"`
	Total    int            `json:"total"`
}

// handleReadTree serves one level of a finalized fcontainer's media tree as
// decoded JSON: the requested node (?node=, default root) plus a page of its
// immediate children (?offset=&limit=). The fblock-status/fblock-live pages
// walk the tree by repeatedly calling this rather than fetching the whole
// TOC and decoding it client-side (see this package's doc on why: decoding
// belongs at the lowest layer that already has toc.Columns in hand).
func (s *HttpApiServer) handleReadTree(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	nodeID := uint32(0)
	if q := r.URL.Query().Get("node"); q != "" {
		v, err := strconv.ParseUint(q, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid node %q: %w", q, err))
			return
		}
		nodeID = uint32(v)
	}
	if nodeID >= columns.N {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: node %d out of range (N=%d)", nodeID, columns.N))
		return
	}

	offset := 0
	if q := r.URL.Query().Get("offset"); q != "" {
		v, err := strconv.Atoi(q)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid offset %q", q))
			return
		}
		offset = v
	}
	limit := defaultTreeChildLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		v, err := strconv.Atoi(q)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid limit %q", q))
			return
		}
		limit = min(v, maxTreeChildLimit)
	}

	children, childCount := subtreeChildren(columns, nodeID)
	total := len(children)
	lo := min(offset, total)
	hi := min(lo+limit, total)

	page := make([]treeNodeJSON, hi-lo)
	for i, cid := range children[lo:hi] {
		page[i] = tocNodeJSON(columns, cid, childCount[cid-nodeID])
	}

	writeJSON(w, http.StatusOK, treeLevelResponse{
		Node:     tocNodeJSON(columns, nodeID, total),
		Children: page,
		Offset:   lo,
		Total:    total,
	})
}
