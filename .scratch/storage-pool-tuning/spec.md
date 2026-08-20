# Per-storage write-buffer pool tuning ("fblocks in RAM")

Status: open — grilled 2026-08-20, issues filed 2026-08-20, not yet implemented

## Summary

Request (translated from Russian): storage add/edit pages need a "count of
fblocks in RAM" field, and farcd must use that value and persist it to its
own config.

Grilling found the only existing concept literally matching "N fblocks
held in memory at once" is the existing write-buffer pool
(`internal/storage/pool.go`'s `Pool`, tuned by `PoolTuning{Size, WarningAt,
BackpressureAt}`) — today a single value global to the whole farcd process
(env vars `FARC_STORAGE_POOL_SIZE`/`_WARNING_AT`/`_BACKPRESSURE_AT`,
`internal/config.Config.StoragePoolSize` etc., default 4/2/4), applied
identically to every Storage farcd opens. No read-side fblock cache exists
anywhere in the codebase — that was considered and rejected as the
interpretation.

## Decisions (settled during grilling)

1. **Semantics**: "fblocks in RAM" = `storage.PoolTuning.Size` — the
   number of fblock-sized write buffers the pool holds concurrently
   (filling, queued, or actively being written). Becomes a **per-storage**
   setting instead of the current global env value.
2. **Apply timing**: changing the value is config-only — it takes effect
   the next time farcd opens that Storage (i.e. after a farcd **restart**).
   No live Pool resize, no per-storage reopen-without-restart mechanism.
3. **No env fallback**: `FARC_STORAGE_POOL_SIZE`/`_WARNING_AT`/
   `_BACKPRESSURE_AT` and `Config.StoragePoolSize`/etc. are removed
   entirely (breaking change — acceptable, still in development). The
   per-storage config field is the only source; the UI always sends an
   explicit value (default 4 pre-filled on the create page). A config
   entry missing the field (hand-edited, or pre-dating this feature) falls
   back to a hardcoded default of `Size=4, WarningAt=2, BackpressureAt=4`
   — the same numbers `storage.DefaultPoolTuning()` already uses today, so
   this is a silent no-op for anyone who never customized the old env
   vars.
4. **Warning/backpressure exposed too**: `WarningAt` and `BackpressureAt`
   become UI-editable fields as well (not auto-scaled from `Size`), entered
   as raw slot counts. A `form-text` helper under each shows the computed
   percentage of `Size` (display only, not an input) — e.g. "2 of 4 slots
   (50%)".
5. **Grouped save**: `Size`/`WarningAt`/`BackpressureAt` save together as
   one atomic unit (one "Save" button, one PATCH request, validated as a
   combination) rather than three independent fields like
   `retention_days`/`write_mode` today.
6. **Validation**: non-strict ordering, `1 ≤ WarningAt ≤ BackpressureAt ≤
   Size`, checked on the fully-resolved (post-default) values.
7. **RAM-usage helper**: the `Size` field gets its own `form-text` helper
   showing `Size × FblockSize` as an estimated GiB figure — informational
   only, no hard upper-bound cap.
8. **GET /storages round-trip**: extended to return the actual values in
   effect, so the Edit page can pre-populate real values before the
   operator saves the group (otherwise saving without editing could
   silently overwrite a previous customization with UI defaults) — a gap
   `retention_days`/`write_mode` already has and this feature deliberately
   does not inherit.
   - Design note (not separately grilled, decided while writing the
     issues): "actual values in effect" means the *registered* config
     value, not necessarily the live `Unit`'s in-memory `Pool` — mirrors
     the existing `name` field, which the registry already caches
     independently of the live `Unit` (`StorageRegistry.SetName`) so a
     PATCH is reflected in the next GET immediately, without waiting for
     decision 2's restart. Reusing that exact pattern for `Pool`
     (`StorageRegistry.SetPoolTuning`) avoids re-showing a stale/live value
     that would make a second grouped edit silently undo a still-pending
     first one.

## Issues

- `issues/01` — `internal/config` + `internal/storage`: schema fields,
  remove global env, validation, accessors, farcd's `openStorage` wiring
- `issues/02` — `internal/api`: `POST /storages` applies+persists the
  group, `GET /storages` returns actual values
- `issues/03` — `internal/api`: `PATCH /storages/{id}` grouped update
- `issues/04` — `web`: `StorageNewPage.tsx` — three new fields + helpers
- `issues/05` — `web`: `StorageEditPage.tsx` — three new fields, grouped
  save, real-value prefill

Implement in order — each backend issue's compile depends on the previous
one's exported surface (accessors before API usage, hooks before farcd
wiring); the frontend issues depend on 02/03's wire shape.
