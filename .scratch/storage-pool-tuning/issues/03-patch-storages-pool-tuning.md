# 03 — PATCH /storages/{id}: grouped pool-tuning update

Status: resolved
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

## Comments

Implemented as specced: `patchStorageRequest.Pool *storage.PoolTuning`,
validated via `Validate()` then applied via `SetPoolTuning` +
`onStoragePoolUpdated`; `onStoragePoolUpdated` added as its own hook on
`HttpApiServer` (kept separate from `onStorageUpdated`'s name-only
signature); `persistUpdatedStoragePool` mirrors `persistUpdatedStorage`.
Tests cover: PATCH's pool visible immediately in a subsequent GET (registry
cache, no restart); invalid ordering rejected 400 and leaves the registry's
previous value untouched; a pool-less PATCH (existing `retention_days`-only
case) leaves it unchanged; the hook receives id+pool; and a farcd-level test
that a PATCH's pool values survive a restart via the saved config file.

**Correction, caught by a second-opinion review after the above first
landed:** step 2's claim above ("`Validate()`, which already applies
`withDefaults()` internally, ... is exactly what goes to registry+config")
was wrong -- `Validate()` only returns `error`; the resolved copy it builds
internally to check the ordering was discarded, so a *partial* group (e.g.
`{"pool":{"Size":8}}`, leaving `WarningAt`/`BackpressureAt` at their JSON
zero value) validated fine but then stored `{8,0,0}` verbatim in the
registry/config -- a real, wrong value indistinguishable from "unset",
unlike the create path (issue 02), which was already correct because it
reads back `unit.PoolTuning()` post-`Open` rather than trusting the
request. Fixed by adding `PoolTuning.Resolved()` (issue 01's package,
`internal/storage/tuning.go` -- returns `t.withDefaults()`, i.e. exposes
what `Validate()` already computed but threw away) and using
`req.Pool.Resolved()` -- not `*req.Pool` -- for both `SetPoolTuning` and
`onStoragePoolUpdated`. Added
`TestHandlePatchStorage_Pool_PartialGroupIsResolvedBeforeStoring` (a
partial `{Size:8}` group must resolve to `{8,2,4}` in the registry, not
`{8,0,0}`) and a `TestPoolTuning_Resolved` table test in
`internal/storage/tuning_test.go`.

`go build ./...`, `go test ./...`, and `golangci-lint run ./...` all clean.
