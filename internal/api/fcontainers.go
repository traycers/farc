package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/toc"
)

func (s *HttpApiServer) resolveUnitAndUUID(w http.ResponseWriter, r *http.Request) (*storage.Unit, [16]byte, bool) {
	unit, _, ok := s.resolveUnit(w, r)
	if !ok {
		return nil, [16]byte{}, false
	}
	uuid, err := parseUUID(mux.Vars(r)["uuid"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return nil, [16]byte{}, false
	}
	return unit, uuid, true
}

// resolveUnit resolves the {id} path variable to its registered Storage, or
// writes a 404 and returns ok=false. Shared by every handler that only
// needs a plain storage lookup (no fcontainer uuid alongside it, unlike
// resolveUnitAndUUID above).
func (s *HttpApiServer) resolveUnit(w http.ResponseWriter, r *http.Request) (*storage.Unit, string, bool) {
	id := mux.Vars(r)["id"]
	unit, ok := s.reg.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", id))
		return nil, id, false
	}
	return unit, id, true
}

// handleReadTOC serves a fcontainer's TOC section, re-encoded from the
// already-decoded (and thus already-validated) Columns rather than the raw
// disk read — see api.go's package doc for why.
func (s *HttpApiServer) handleReadTOC(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	buf, err := toc.Encode(columns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(buf)
}

// parseRanges parses "off:len,off:len,..." into storage.Range values (ADR-004
// §8.4's ranged-read request, as a query parameter here).
func parseRanges(s string) ([]storage.Range, error) {
	parts := strings.Split(s, ",")
	out := make([]storage.Range, 0, len(parts))
	for _, p := range parts {
		off, length, found := strings.Cut(p, ":")
		if !found {
			return nil, fmt.Errorf("api: invalid range %q, want off:len", p)
		}
		offset, err := strconv.ParseUint(off, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("api: invalid range offset %q: %w", off, err)
		}
		size, err := strconv.ParseUint(length, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("api: invalid range length %q: %w", length, err)
		}
		out = append(out, storage.Range{Offset: offset, Size: size})
	}
	return out, nil
}

// handleReadContent serves either specific byte ranges of a fcontainer's
// Content section (?ranges=off:len,...) or, with no ranges query, the whole
// section (ADR-003's full export) — both as raw bytes, concatenated in the
// order requested, matching ADR-004's "farcd just returns bytes at given
// offsets" model.
func (s *HttpApiServer) handleReadContent(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}

	rangesParam := r.URL.Query().Get("ranges")
	if rangesParam == "" {
		size, err := unit.ContentSize(uuid)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		buf, err := unit.ReadRange(uuid, 0, uint64(size))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(buf)
		return
	}

	ranges, err := parseRanges(rangesParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	bufs, err := unit.ReadRanges(uuid, ranges)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	for _, buf := range bufs {
		_, _ = w.Write(buf)
	}
}

type setProtectedRequest struct {
	Value bool `json:"value"`
}

// handleSetProtected implements POST .../protected {value} — resolves uuid
// to its physical index (SetProtected itself is index-keyed, since
// protected is a per-fblock catalog flag, not a fcontainer-identity one).
func (s *HttpApiServer) handleSetProtected(w http.ResponseWriter, r *http.Request) {
	unit, uuid, ok := s.resolveUnitAndUUID(w, r)
	if !ok {
		return
	}
	var req setProtectedRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	idx, ok := unit.ResolveUUID(uuid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: fcontainer %x not found (not Ready)", uuid))
		return
	}
	err = unit.Index().SetProtected(idx, req.Value)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
