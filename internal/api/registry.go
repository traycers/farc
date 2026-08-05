package api

import (
	"fmt"
	"sort"
	"sync"

	"traycers/farc/internal/storage"
)

// StorageInfo is StorageRegistry's public listing shape for GET /storages.
type StorageInfo struct {
	ID       string           `json:"id"`
	Path     string           `json:"path"`
	Name     string           `json:"name,omitempty"`
	Geometry storage.Geometry `json:"geometry"`
}

type registeredStorage struct {
	unit *storage.Unit
	path string
	name string
}

// StorageRegistry maps operator-chosen storage ids to already-open
// storage.Units. It only tracks already-open Units — POST /storages does the
// actual Init+Open before registering (see api.go's package doc).
type StorageRegistry struct {
	mu    sync.RWMutex
	units map[string]*registeredStorage
}

// NewStorageRegistry creates an empty registry.
func NewStorageRegistry() *StorageRegistry {
	return &StorageRegistry{units: make(map[string]*registeredStorage)}
}

// Register adds unit under id. It is an error to reuse an id already
// registered — the caller (POST /storages' handler) is expected to close
// unit and report a conflict in that case, rather than silently replacing a
// live Storage some other request may be using.
func (r *StorageRegistry) Register(id string, unit *storage.Unit, path string, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.units[id]; exists {
		return fmt.Errorf("api: storage id %q already registered", id)
	}
	r.units[id] = &registeredStorage{unit: unit, path: path, name: name}
	return nil
}

// SetName renames an already-registered storage in place. Returns false if
// id isn't registered.
func (r *StorageRegistry) SetName(id, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.units[id]
	if !ok {
		return false
	}
	e.name = name
	return true
}

// Get returns the Unit registered under id, if any.
func (r *StorageRegistry) Get(id string) (*storage.Unit, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.units[id]
	if !ok {
		return nil, false
	}
	return e.unit, true
}

// List returns every registered storage's info, sorted by id for a stable
// response across calls.
func (r *StorageRegistry) List() []StorageInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]StorageInfo, 0, len(r.units))
	for id, e := range r.units {
		out = append(out, StorageInfo{ID: id, Path: e.path, Name: e.name, Geometry: e.unit.Geometry()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
