package api

import (
	"fmt"
	"net/http"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
)

// createStorageRequest is POST /storages' body. Geometry/Params mirror
// storage.Geometry/fblock.Params field-for-field — no separate wire schema,
// since both are already plain data structs meant to be operator-supplied
// (docs/docs/archive/04-storage-operations.md §2.1's config, here as an API
// body instead of a static config file).
type createStorageRequest struct {
	ID          string           `json:"id"`
	Path        string           `json:"path"`
	Geometry    storage.Geometry `json:"geometry"`
	Params      fblock.Params    `json:"params"`
	Force       bool             `json:"force"`
	CatalogPath string           `json:"catalog_path"`
	// Backend selects ioengine.Options.Backend ("direct"/"standard"/"");
	// exposed mainly for tests (tmpfs doesn't support O_DIRECT).
	Backend string `json:"backend"`
}

// handleCreateStorage runs Initializer inline (ADR-006 makes this cheap —
// only fblock 0 is actually written) and registers the resulting open Unit.
func (s *HttpApiServer) handleCreateStorage(w http.ResponseWriter, r *http.Request) {
	var req createStorageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ID == "" || req.Path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: id and path are required"))
		return
	}
	if _, exists := s.reg.Get(req.ID); exists {
		writeError(w, http.StatusConflict, fmt.Errorf("api: storage id %q already registered", req.ID))
		return
	}

	size := int64(req.Geometry.FblockSize) * int64(req.Geometry.N)
	if err := storage.CreateSizedFile(req.Path, size, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	backend, err := ioengine.Open(req.Path, ioengine.Options{Backend: req.Backend})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	tuning := storage.DefaultEngineTuning()
	initCfg := storage.InitConfig{
		Geometry:    req.Geometry,
		Params:      req.Params,
		Now:         uint64(time.Now().UnixNano()),
		Force:       req.Force,
		CatalogPath: req.CatalogPath,
		Tuning:      tuning,
	}
	if err := storage.Init(backend, initCfg); err != nil {
		_ = backend.Close()
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	unit, err := storage.Open(storage.OpenConfig{Backend: backend, CatalogPath: req.CatalogPath, Tuning: tuning})
	if err != nil {
		_ = backend.Close()
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	if err := s.reg.Register(req.ID, unit, req.Path); err != nil {
		_ = unit.Close()
		writeError(w, http.StatusConflict, err)
		return
	}

	if err := s.onStorageCreated(req.ID, req.Path, req.CatalogPath); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist storage %q: %w", req.ID, err))
		return
	}

	writeJSON(w, http.StatusCreated, StorageInfo{ID: req.ID, Path: req.Path, Geometry: req.Geometry})
}

func (s *HttpApiServer) handleListStorages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.reg.List())
}

// patchStorageRequest is PATCH /storages/{id}'s body — the only two Storage
// params documented as operator-mutable at runtime (index.Manager already
// has setters for both; write_mode is included alongside retention_days for
// the same reason, even though the sketch only names retention_days).
type patchStorageRequest struct {
	RetentionDays *int64  `json:"retention_days,omitempty"`
	WriteMode     *string `json:"write_mode,omitempty"`
}

func (s *HttpApiServer) handlePatchStorage(w http.ResponseWriter, r *http.Request) {
	unit, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", r.PathValue("id")))
		return
	}
	var req patchStorageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RetentionDays != nil {
		unit.Index().SetRetentionDays(*req.RetentionDays)
	}
	if req.WriteMode != nil {
		unit.Index().SetWriteMode(*req.WriteMode)
	}
	w.WriteHeader(http.StatusNoContent)
}
