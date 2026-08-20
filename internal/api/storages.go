package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storage"
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
	// Pool sizes the write-buffer pool this Storage's Unit is opened with --
	// how many fblocks may be filling/queued/actively-written in RAM at
	// once (storage.PoolTuning). Zero fields resolve to
	// storage.DefaultPoolTuning() (4/2/4).
	Pool storage.PoolTuning `json:"pool"`
}

// createStorage is handleCreateStorage's HTTP-free core: Init+Open+Register
// a new Storage and persist it via onStorageCreated. A returned error may be
// an *apiError requesting a specific status (400/409); callers should render
// it through writeAPIError with 500 as the default.
func (s *HttpApiServer) createStorage(req createStorageRequest) (StorageInfo, error) {
	if req.ID == "" || req.Path == "" {
		return StorageInfo{}, apiErr(http.StatusBadRequest, errIDAndPathRequired)
	}
	if _, exists := s.reg.Get(req.ID); exists {
		return StorageInfo{}, apiErr(http.StatusConflict, fmt.Errorf("api: storage id %q already registered", req.ID))
	}
	err := req.Pool.Validate()
	if err != nil {
		return StorageInfo{}, apiErr(http.StatusBadRequest, err)
	}

	size := int64(req.Geometry.FblockSize) * int64(req.Geometry.N)
	err = storage.CreateSizedFile(req.Path, size, 0o600)
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

	unit, err := storage.Open(storage.OpenConfig{Backend: backend, CatalogPath: req.CatalogPath, Tuning: tuning, PoolTuning: req.Pool})
	if err != nil {
		_ = backend.Close()
		return StorageInfo{}, err
	}
	// resolvedPool, not req.Pool, which may have zero fields storage.Open
	// already resolved via PoolTuning.withDefaults() -- this is what's
	// actually in effect, for both the registry cache and the response.
	resolvedPool := unit.PoolTuning()

	err = s.reg.Register(req.ID, unit, req.Path, req.Name, resolvedPool)
	if err != nil {
		_ = unit.Close()
		return StorageInfo{}, apiErr(http.StatusConflict, err)
	}

	err = s.onStorageCreated(req.ID, req.Path, req.CatalogPath, req.Name, resolvedPool)
	if err != nil {
		return StorageInfo{}, fmt.Errorf("api: persist storage %q: %w", req.ID, err)
	}

	return StorageInfo{ID: req.ID, Path: req.Path, Name: req.Name, Geometry: req.Geometry, Pool: resolvedPool}, nil
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
// from reg and closes the underlying Unit. It does not persist anything
// itself -- handleRemoveStorage calls onStorageRemoved first and only then
// this, mirroring how handleRemoveChannel's caller ordering keeps
// IngestManager and the config file from ever disagreeing about what exists.
func (s *HttpApiServer) removeStorage(id string) error {
	unit, ok := s.reg.Unregister(id)
	if !ok {
		return apiErr(http.StatusNotFound, fmt.Errorf("api: unknown storage %q", id))
	}
	return unit.Close()
}

// handleRemoveStorage is DELETE /storages/{id}: persists the removal via
// onStorageRemoved first (same ordering removeStorage's own doc comment
// requires of every caller), then tears the Storage down. A persist
// failure leaves the Storage fully intact rather than half torn down.
//
// Refuses (409) while any channel still targets this storage: storage.Unit.
// Close's own doc comment requires no WriteFcontainer call be in flight when
// it's called, and a still-running channel's ChannelIngest goroutine can
// call it (via Recorder) at any time -- any caller must remove every such
// channel first (DELETE /channels/{id}) rather than relying on this 409,
// which is a last-resort guard, not the intended primary path.
//
// This check is a best-effort snapshot, not a lock spanning
// StorageRegistry and IngestManager: a channel attaching to id concurrently
// with this call can still race past it. Acceptable for now given every
// caller is expected to remove channels first through the same instance of
// this HttpApiServer; revisit with real cross-manager coordination if
// concurrent unrelated callers become a real scenario.
func (s *HttpApiServer) handleRemoveStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.reg.Get(id); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q", id))
		return
	}
	if s.ing != nil {
		for _, c := range s.ing.List() {
			if c.StorageID == id {
				writeError(w, http.StatusConflict, fmt.Errorf("api: storage %q still has channel %d attached", id, c.Channel))
				return
			}
		}
	}
	err := s.onStorageRemoved(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist storage %q removal: %w", id, err))
		return
	}
	err = s.removeStorage(id)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	// Pool, if present, updates Size/WarningAt/BackpressureAt together as
	// one atomic group -- persisted to config immediately but applied to
	// the live Unit only after farcd's next restart (Pool has no resize
	// method), though reflected in GET /storages immediately via the
	// registry cache (StorageRegistry.SetPoolTuning), same mechanism
	// createStorage uses.
	Pool *storage.PoolTuning `json:"pool,omitempty"`
}

func (s *HttpApiServer) handlePatchStorage(w http.ResponseWriter, r *http.Request) {
	unit, id, ok := s.resolveUnit(w, r)
	if !ok {
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
	if req.Pool != nil {
		err := req.Pool.Validate()
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		// Resolved, not *req.Pool verbatim: a partial group (e.g. only Size
		// set) must store what it actually resolves to, or the registry
		// (and therefore GET /storages) would show a stale zero for a field
		// that's actually defaulting to something else -- same reasoning as
		// createStorage's own resolvedPool.
		resolvedPool := req.Pool.Resolved()
		s.reg.SetPoolTuning(id, resolvedPool)
		err = s.onStoragePoolUpdated(id, resolvedPool)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist storage %q pool tuning: %w", id, err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
