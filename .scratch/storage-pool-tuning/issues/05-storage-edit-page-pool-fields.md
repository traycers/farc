# 05 — StorageEditPage.tsx: pool-tuning fields, grouped save, real-value prefill

Status: open
Blocked by: 03

## Task

Add the same three fields to the edit page (spec decisions 2, 4, 5, 6, 7,
8) as one grouped, independently-saved unit, pre-filled from `GET
/storages`' now-accurate `pool` field (issues 02/03), not a UI default
like `retentionDays`/`writeMode` currently are.

### `web/src/api/farcd.ts`

1. `patchStorage`'s inline patch type (lines 65-68): add `pool?:
   { Size: number; WarningAt: number; BackpressureAt: number }` (or import
   the `PoolTuning` type added in issue 04's step 1 — same shape either
   way, whichever issue lands first).

### `web/src/pages/storages/StorageEditPage.tsx`

2. Add three pieces of state, initialized from the fetched `storage.pool`
   in the existing `listStorages().then(...)` block (lines 37-47) —
   **unlike** `retentionDays`/`writeMode` (lines 33-34, whose comment at
   lines 49-52 explains why they *can't* be preselected today), this is
   real data now available:
   ```ts
   const [poolSize, setPoolSize] = useState(4)
   const [poolWarningAt, setPoolWarningAt] = useState(2)
   const [poolBackpressureAt, setPoolBackpressureAt] = useState(4)
   ```
   and inside the `.then((all) => { ... if (found) { ... } })` block
   (lines 42-44), add:
   ```ts
   setPoolSize(found.pool.Size)
   setPoolWarningAt(found.pool.WarningAt)
   setPoolBackpressureAt(found.pool.BackpressureAt)
   ```
3. Add a single grouped save handler, alongside `onSaveRetention`/
   `onSaveName`/`onSaveWriteMode` (lines 53-85):
   ```ts
   async function onSavePool() {
     setError(null)
     setStatus(null)
     try {
       await patchStorage(id!, {
         pool: { Size: poolSize, WarningAt: poolWarningAt, BackpressureAt: poolBackpressureAt },
       })
       setStatus('pool tuning saved (takes effect after farcd restart)')
     } catch (e) {
       setError(String(e))
     }
   }
   ```
   the status message calls out spec decision 2's restart requirement
   explicitly, since nothing else on this page has that caveat.
4. Render the three inputs together under one block with a single "Save"
   button (matching decision 5 — contrast with `name`/`retentionDays`,
   which each get their own button at lines 146-172), placed in the same
   "Mutable settings" card (after the existing `write mode` field, lines
   173-191). Reuse the same percentage/RAM `form-text` helper pattern from
   issue 04's step 6 (percentage for warning/backpressure, RAM estimate
   for size — using `storage.geometry.FblockSize`, already in scope via
   the fetched `storage` object, no new fetch needed).
5. Update the "Everything above is fixed at creation time" copy (lines
   142-144) — it's no longer accurate once this group is added below it.

## Tests

`StorageEditPage.test.tsx` (exists — check current cases first): add a
case asserting the three fields are pre-filled from the mocked
`listStorages()` response's `pool` field (not a hardcoded UI default,
unlike the existing `retentionDays`/`writeMode` cases); a case for the
grouped save calling `patchStorage` once with all three current values
under a single `pool` key; and a case for the percentage/RAM helper text
rendering correctly for the fetched values.

## Verify

`cd web && npx tsc -b && npx vitest run`
