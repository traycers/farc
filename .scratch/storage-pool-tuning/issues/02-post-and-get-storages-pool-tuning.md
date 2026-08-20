# 02 — POST /storages applies+persists per-storage pool tuning; GET /storages returns it

Status: open
Blocked by: 01

## Task

Wire the schema/accessors from issue 01 into the HTTP layer: creating a
storage now accepts `Size`/`WarningAt`/`BackpressureAt`, and every storage
listing reports the values actually in effect (spec decisions 1, 6, 8).

### `internal/api/registry.go`

1. `registeredStorage` (lines 19-23): add a `pool storage.PoolTuning`
   field, alongside `path`/`name`.
2. `Register` (lines 42-50): add a `pool storage.PoolTuning` parameter,
   stored on the new entry — mirrors how `path`/`name` are already
   explicit params rather than read off `unit`.
3. Add `SetPoolTuning`, mirroring `SetName` (lines 52-63):
   ```go
   // SetPoolTuning updates an already-registered storage's cached pool
   // tuning in place. Returns false if id isn't registered.
   func (r *StorageRegistry) SetPoolTuning(id string, pool storage.PoolTuning) bool {
       r.mu.Lock()
       defer r.mu.Unlock()
       e, ok := r.units[id]
       if !ok {
           return false
       }
       e.pool = pool
       return true
   }
   ```
4. `StorageInfo` (lines 12-17): add `Pool storage.PoolTuning \`json:"pool"\``.
5. `List()` (lines 94-103): populate it from `e.pool` (registry-cached),
   **not** `e.unit.PoolTuning()` — spec decision 8's design note: the
   registry's cached value reflects the latest PATCH immediately, while
   the live `Unit`'s own value only changes after the next open (issue 03
   keeps these in sync on every mutation).

### `internal/api/storages.go`

6. `createStorageRequest` (lines 24-37): add `Pool storage.PoolTuning
   \`json:"pool"\`` next to `Geometry`/`Params`.
7. `createStorage` (lines 43-95): after the existing id/path checks (lines
   44-49), validate `req.Pool.Validate()` (400 on failure, same
   `apiErr(http.StatusBadRequest, ...)` pattern as `errIDAndPathRequired`).
   Thread it into the `storage.Open` call (line 77):
   `storage.OpenConfig{Backend: backend, CatalogPath: req.CatalogPath,
   Tuning: tuning, PoolTuning: req.Pool}`. After `Open` succeeds, read back
   the resolved values — `resolvedPool := unit.PoolTuning()` — and use
   those (not `req.Pool`, which may have zero fields) for both
   `s.reg.Register(req.ID, unit, req.Path, req.Name, resolvedPool)`
   (updated call site per step 2) and the response:
   `StorageInfo{..., Pool: resolvedPool}` (line 94).
8. `onStorageCreated`'s persistence hook call (line 89) also needs
   `resolvedPool` threaded through — see step 9/11 below for the matching
   signature change.

### `internal/api/server.go`

9. `onStorageCreated` field/default/setter (lines 26, 49, 60-73): add a
   `pool storage.PoolTuning` parameter to the func type, its no-op
   default, and `SetOnStorageCreated`'s signature — mirrors the existing
   `id, path, catalogPath, name` params, just one more.

### `internal/farcd/farcd.go`

10. `New()`'s storage-opening loop (lines 120-133): the
    `f.registry.Register` call (line 127) needs the new `pool` arg — use
    `unit.PoolTuning()` right after `openStorage` returns (same "resolved,
    not raw config" reasoning as step 7, covers the case where an old
    config entry has `PoolSize=0` and the hardcoded default silently
    applied).
11. `persistNewStorage` (lines 332-351): add a `pool storage.PoolTuning`
    parameter, store it on the new `config.Storage{...}` literal (line
    339): `PoolSize: pool.Size, PoolWarningAt: pool.WarningAt,
    PoolBackpressureAt: pool.BackpressureAt`. The
    `apiServer.SetOnStorageCreated(f.persistNewStorage)` call (line 184)
    needs no edit itself (bare method reference), but `persistNewStorage`'s
    new signature must match the hook type from step 9.

## Tests

- `internal/api`: existing storages test file gets cases: `POST /storages`
  with an explicit `pool` object round-trips through `GET /storages`;
  omitting `pool` in the request still produces resolved `4/2/4` in the
  response (zero-value defaulting); an invalid ordering (e.g. `size` less
  than `backpressure_at`) is rejected with 400.
- `internal/farcd`: existing farcd-level test (if any covers
  `persistNewStorage`) gets a case asserting the created storage's
  `pool_size`/etc. land in the saved config file.

## Verify

`go build ./... && go test ./internal/api/... ./internal/farcd/...`
