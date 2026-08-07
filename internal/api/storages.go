package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
)

// errIDAndPathRequired is handleCreateStorage's 400 response for its two
// hand-validated required fields (everything else in createStorageRequest
// is either optional or validated downstream by storage.Init).
var errIDAndPathRequired = errors.New("api: id and path are required")

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
	// Name is an optional human-readable label -- purely cosmetic, no
	// uniqueness constraint, id remains the only identity key.
	Name string `json:"name,omitempty"`
}

// createStorage is handleCreateStorage's HTTP-free core: Init+Open+Register
// a new Storage and persist it via onStorageCreated. Shared with
// archives.go's archives_setup, which runs the exact same sequence before
// going on to add channels/set ttl in the same request. A returned error may
// be an *apiError requesting a specific status (400/409); callers should
// render it through writeAPIError with 500 as the default.
func (s *HttpApiServer) createStorage(req createStorageRequest) (StorageInfo, error) {
	if req.ID == "" || req.Path == "" {
		return StorageInfo{}, apiErr(http.StatusBadRequest, errIDAndPathRequired)
	}
	if _, exists := s.reg.Get(req.ID); exists {
		return StorageInfo{}, apiErr(http.StatusConflict, fmt.Errorf("api: storage id %q already registered", req.ID))
	}

	size := int64(req.Geometry.FblockSize) * int64(req.Geometry.N)
	err := storage.CreateSizedFile(req.Path, size, 0o600)
	if err != nil {
		return StorageInfo{}, err
	}

	backend, err := ioengine.Open(req.Path, ioengine.Options{Backend: req.Backend})
	if err != nil {
		return StorageInfo{}, err
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
	err = storage.Init(backend, initCfg)
	if err != nil {
		_ = backend.Close()
		return StorageInfo{}, err
	}

	unit, err := storage.Open(storage.OpenConfig{Backend: backend, CatalogPath: req.CatalogPath, Tuning: tuning})
	if err != nil {
		_ = backend.Close()
		return StorageInfo{}, err
	}

	err = s.reg.Register(req.ID, unit, req.Path, req.Name)
	if err != nil {
		_ = unit.Close()
		return StorageInfo{}, apiErr(http.StatusConflict, err)
	}

	err = s.onStorageCreated(req.ID, req.Path, req.CatalogPath, req.Name)
	if err != nil {
		return StorageInfo{}, fmt.Errorf("api: persist storage %q: %w", req.ID, err)
	}

	return StorageInfo{ID: req.ID, Path: req.Path, Name: req.Name, Geometry: req.Geometry}, nil
}

// handleCreateStorage runs Initializer inline (ADR-006 makes this cheap —
// only fblock 0 is actually written) and registers the resulting open Unit.
func (s *HttpApiServer) handleCreateStorage(w http.ResponseWriter, r *http.Request) {
	var req createStorageRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	info, err := s.createStorage(req)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

// removeStorage tears down an already-registered Storage: unregisters it
// from reg and closes the underlying Unit. It does not touch any channel or
// config-file state -- callers (archives.go's archives_detach) are
// responsible for removing the Storage's channels first and persisting the
// removal via onStorageRemoved before calling this, mirroring how
// handleRemoveChannel's caller ordering keeps IngestManager and the config
// file from ever disagreeing about what exists.
func (s *HttpApiServer) removeStorage(id string) error {
	unit, ok := s.reg.Unregister(id)
	if !ok {
		return apiErr(http.StatusNotFound, fmt.Errorf("api: unknown storage %q", id))
	}
	return unit.Close()
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
	Name          *string `json:"name,omitempty"`
}

func (s *HttpApiServer) handlePatchStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	unit, ok := s.reg.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", id))
		return
	}
	var req patchStorageRequest
	err := decodeJSON(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RetentionDays != nil {
		unit.Index().SetRetentionDays(*req.RetentionDays)
	}
	if req.WriteMode != nil {
		unit.Index().SetWriteMode(*req.WriteMode)
	}
	if req.Name != nil {
		s.reg.SetName(id, *req.Name)
		err := s.onStorageUpdated(id, *req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist storage %q rename: %w", id, err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
