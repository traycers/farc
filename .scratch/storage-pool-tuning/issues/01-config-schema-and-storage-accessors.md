# 01 — Per-storage pool-tuning config schema + storage-package accessors

Status: open

## Task

Foundation for making the write-buffer pool's `Size`/`WarningAt`/
`BackpressureAt` a per-storage, config-persisted setting instead of
today's single global env value (see spec decisions 1-3).

### `internal/config/config.go`

1. Add three fields to `Storage` (currently `ID`/`Path`/`CatalogPath`/
   `Name`, lines 95-100):
   ```go
   type Storage struct {
       ID          string `json:"id"`
       Path        string `json:"path"`
       CatalogPath string `json:"catalog_path,omitempty"`
       Name        string `json:"name,omitempty"`
       PoolSize           int `json:"pool_size,omitempty"`
       PoolWarningAt      int `json:"pool_warning_at,omitempty"`
       PoolBackpressureAt int `json:"pool_backpressure_at,omitempty"`
   }
   ```
   Zero (omitted) means "use the hardcoded default" — same convention
   `storage.PoolTuning`'s own zero-field handling already uses (see
   `internal/storage/tuning.go`'s `withDefaults`).
2. Remove `Config.StoragePoolSize`/`StoragePoolWarningAt`/
   `StoragePoolBackpressureAt` (lines 144-156, including their doc
   comment) and the matching `loadEnv` reads of `FARC_STORAGE_POOL_SIZE`/
   `_WARNING_AT`/`_BACKPRESSURE_AT` (lines 261-272) — this was the sole
   global-per-process source, now replaced by the per-storage fields above
   (spec decision 3, breaking change, acceptable per project stage).
3. `cfg.validate()`'s storages loop (lines 311-323): add an ordering check
   for the new trio, resolved against hardcoded defaults matching
   `storage.DefaultPoolTuning()`'s numbers (`config.go` can't import
   `internal/storage` — see the package doc's "stdlib-only" rule — so
   duplicate the three constants as local `const`s with a comment pointing
   at `storage.DefaultPoolTuning()` to keep them in sync intentionally):
   ```go
   const (
       defaultPoolSize           = 4 // keep in sync with storage.DefaultPoolTuning()
       defaultPoolWarningAt      = 2
       defaultPoolBackpressureAt = 4
   )
   ```
   then, per storage `s`, resolve zero fields to these defaults and check
   `1 <= warningAt && warningAt <= backpressureAt && backpressureAt <= size`,
   returning a `storages[%d]: pool tuning must satisfy 1 <= warning_at <=
   backpressure_at <= size` error otherwise (same message shape as the
   existing `id`/`path`/`duplicate id` checks right above it).

### `internal/config/config_test.go`

4. Update/remove any test asserting `StoragePoolSize`/`_WarningAt`/
   `_BackpressureAt` round-trip via env or JSON (grep the file for
   `StoragePool`/`FARC_STORAGE_POOL` first). Add a test for the new
   `validate()` ordering check (a passing case and a violating case), and
   a round-trip test that `pool_size`/`pool_warning_at`/
   `pool_backpressure_at` survive `Save`+`Load`.

### `internal/storage/tuning.go`

5. Add an exported validator next to `PoolTuning` (below `withDefaults`,
   around line 76 — needs a new `"fmt"` import in this file):
   ```go
   // Validate reports whether t's fully-resolved (default-applied) values
   // satisfy 1 <= WarningAt <= BackpressureAt <= Size — the ordering
   // backpressure's occupancy check (Pool.statusLocked) assumes.
   func (t PoolTuning) Validate() error {
       r := t.withDefaults()
       if r.WarningAt < 1 || r.BackpressureAt < r.WarningAt || r.Size < r.BackpressureAt {
           return fmt.Errorf("storage: pool tuning must satisfy 1 <= warning_at <= backpressure_at <= size (got size=%d warning_at=%d backpressure_at=%d)", r.Size, r.WarningAt, r.BackpressureAt)
       }
       return nil
   }
   ```

### `internal/storage/pool.go`

6. Add an accessor next to `Status` (around lines 96-101):
   ```go
   // Tuning reports the resolved PoolTuning this Pool was constructed with
   // (defaults already applied by newPool) — internal/api's GET /storages
   // uses this at Storage-open time to learn what's actually in effect,
   // spec decision 8.
   func (p *Pool) Tuning() PoolTuning {
       return PoolTuning{Size: p.size, WarningAt: p.warnAt, BackpressureAt: p.pressAt}
   }
   ```

### `internal/storage/unit.go`

7. Add a delegating accessor next to `PoolStatus` (lines 102-104):
   ```go
   // PoolTuning reports the resolved buffer-pool tuning this Unit was
   // opened with.
   func (u *Unit) PoolTuning() PoolTuning { return u.pool.Tuning() }
   ```

### `internal/farcd/farcd.go`

8. `openStorage` (lines 221-241): replace the global-config-sourced
   `PoolTuning` with the per-storage fields:
   ```go
   PoolTuning: storage.PoolTuning{
       Size:           sc.PoolSize,
       WarningAt:      sc.PoolWarningAt,
       BackpressureAt: sc.PoolBackpressureAt,
   },
   ```
   (drop the `cfg.StoragePoolSize`/etc. references — `cfg` is still used
   for `cfg.StorageBackend` just above, so the parameter stays, just with
   one less use).

## Tests

- `internal/config`: `config_test.go` cases from step 4.
- `internal/storage`: new or extended test file cases for
  `PoolTuning.Validate()` — a valid combination, an all-zero combination
  (resolves to defaults, valid), and a few invalid orderings. Whichever
  test file already covers `Pool`/`Unit` construction gets a case
  asserting `Pool.Tuning()`/`Unit.PoolTuning()` echoes back the
  `PoolTuning` a Pool/Unit was constructed/opened with, including the
  zero-value-resolves-to-defaults case.

## Verify

`go build ./... && go test ./internal/config/... ./internal/storage/... ./internal/farcd/...`
