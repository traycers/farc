# 03 — PATCH /storages/{id}: grouped pool-tuning update

Status: open
Blocked by: 02

## Task

Let an operator edit `Size`/`WarningAt`/`BackpressureAt` on an existing
storage as one atomic group (spec decisions 2, 5, 6) — persisted to
config immediately, applied to the live `Unit` only after farcd's next
restart (decision 2), but reflected in `GET /storages` immediately via the
registry cache (decision 8, same mechanism issue 02 wired up for create).

### `internal/api/storages.go`

1. `patchStorageRequest` (lines 180-184): add
   `Pool *storage.PoolTuning \`json:"pool,omitempty"\`` — a pointer to the
   whole group (present means "update all three together", matching
   `RetentionDays *int64`/`WriteMode *string`'s already-pointer-optional
   style, but here guarding one nested struct instead of three independent
   scalars, since the group must be validated and saved as a unit).
2. `handlePatchStorage` (lines 186-212): add a branch alongside the
   existing `RetentionDays`/`WriteMode`/`Name` ones:
   ```go
   if req.Pool != nil {
       if err := req.Pool.Validate(); err != nil {
           writeError(w, http.StatusBadRequest, err)
           return
       }
       s.reg.SetPoolTuning(id, *req.Pool)
       if err := s.onStoragePoolUpdated(id, *req.Pool); err != nil {
           writeError(w, http.StatusInternalServerError, fmt.Errorf("api: persist storage %q pool tuning: %w", id, err))
           return
       }
   }
   ```
   This validates+persists the request's own values directly (no
   `unit.PoolTuning()` read-back here, unlike create) — the live `Unit`
   isn't being reopened, so there's nothing newly "resolved" to read back;
   the operator's input (after `Validate()`, which already applies
   `withDefaults()` internally) is exactly what goes to registry+config.

### `internal/api/server.go`

3. Add a new hook, parallel to `onStorageUpdated` (lines 26-27, 49-50,
   76-85) but for the pool group specifically (kept separate from
   `onStorageUpdated`'s `name`-only signature rather than overloading it):
   ```go
   onStoragePoolUpdated func(id string, pool storage.PoolTuning) error
   ```
   with its own no-op default and `SetOnStoragePoolUpdated` setter,
   mirroring `SetOnStorageUpdated`'s doc comment and structure.

### `internal/farcd/farcd.go`

4. Add `persistUpdatedStoragePool`, mirroring `persistUpdatedStorage`
   (lines 356-375) field-for-field but writing `PoolSize`/
   `PoolWarningAt`/`PoolBackpressureAt` instead of `Name`:
   ```go
   func (f *Farcd) persistUpdatedStoragePool(id string, pool storage.PoolTuning) error {
       _, err := f.withConfigMutation(fmt.Sprintf("persist storage %q pool tuning", id), func() (func(), error) {
           idx := -1
           for i, s := range f.cfg.Storages {
               if s.ID == id {
                   idx = i
                   break
               }
           }
           if idx < 0 {
               return nil, fmt.Errorf("farcd: persist storage %q pool tuning: not present in config", id)
           }
           old := f.cfg.Storages[idx]
           f.cfg.Storages[idx].PoolSize = pool.Size
           f.cfg.Storages[idx].PoolWarningAt = pool.WarningAt
           f.cfg.Storages[idx].PoolBackpressureAt = pool.BackpressureAt
           return func() {
               f.cfg.Storages[idx] = old
           }, nil
       })
       return err
   }
   ```
5. Wire it in `New()` next to the other `SetOn*` calls (lines 184-189):
   `apiServer.SetOnStoragePoolUpdated(f.persistUpdatedStoragePool)`.

## Tests

- `internal/api`: `PATCH /storages/{id}` with a valid `pool` group updates
  the response of a subsequent `GET /storages` immediately (registry
  cache, no restart involved at this test level); an invalid ordering in
  the patch body is rejected 400 and leaves the previous registry/config
  value untouched; omitting `pool` from the patch body leaves it unchanged
  (existing `retention_days`-only patch behavior, unaffected).
- `internal/farcd`: a case asserting a PATCH's pool values land in the
  saved config file (same shape as issue 02's create-side config
  assertion).

## Verify

`go build ./... && go test ./internal/api/... ./internal/farcd/...`
