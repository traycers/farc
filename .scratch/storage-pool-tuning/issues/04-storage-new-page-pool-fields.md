# 04 — StorageNewPage.tsx: pool-tuning fields (Size/WarningAt/BackpressureAt)

Status: open
Blocked by: 02

## Task

Add the three new fields to the create-storage form (spec decisions 1, 4,
6, 7), sent as one `pool` object in `createStorage`'s request body.

### `web/src/api/farcd.ts`

1. Add a `PoolTuning` type next to `Geometry` (lines 5-11), with the same
   "no json tags in Go, so PascalCase" comment:
   ```ts
   // storage.PoolTuning has no json tags in Go either -- PascalCase, same
   // reasoning as Geometry above.
   export type PoolTuning = {
     Size: number
     WarningAt: number
     BackpressureAt: number
   }
   ```
2. `CreateStorageInput` (lines 42-51): add `pool: PoolTuning`.
3. `StorageInfo` (lines 23-28): add `pool: PoolTuning`.

### `web/src/pages/storages/StorageNewPage.tsx`

4. Add three number-input fields next to `maxChannels` (state near line
   64, inputs near lines 198-208) — `poolSize` (default `4`),
   `poolWarningAt` (default `2`), `poolBackpressureAt` (default `4`):
   ```ts
   const [poolSize, setPoolSize] = useState(4)
   const [poolWarningAt, setPoolWarningAt] = useState(2)
   const [poolBackpressureAt, setPoolBackpressureAt] = useState(4)
   ```
5. Pass them through in `onCreate`'s `createStorage` call (lines 78-92):
   `pool: { Size: poolSize, WarningAt: poolWarningAt, BackpressureAt:
   poolBackpressureAt }`.
6. Render, e.g. right after the existing `max channels` field (after line
   208):
   - `pool size (fblocks in RAM)` number input bound to `poolSize`, with a
     `form-text` helper showing the RAM estimate (spec decision 7):
     `≈ {formatGiB(poolSize * fblockSize)} GiB RAM when the pool is full`
     (reuse the existing `formatGiB`/`fblockSize` already defined at
     lines 21-23/61 for the desired-size field).
   - `pool warning at` / `pool backpressure at` number inputs bound to
     `poolWarningAt`/`poolBackpressureAt`, each with a `form-text` helper
     showing the percentage of `poolSize`, e.g.
     `` `${poolWarningAt} of ${poolSize} slots (${Math.round(100 * poolWarningAt / poolSize)}%)` ``
     — guard the division for `poolSize === 0` (show `—` rather than
     `NaN%`/`Infinity%`).

## Tests

No `StorageNewPage.test.tsx` exists yet (grep to confirm before assuming).
Add one: fill the form, assert `createStorage` is called with a `pool`
object matching the three fields' current values (including their
defaults when untouched), and that the two `form-text` helpers render the
expected percentage/GiB strings for a couple of representative
size/threshold combinations (including a `poolSize = 0` edge case for the
percentage helpers, if the input allows clearing to empty/0 — assert it
doesn't crash or show `NaN%`).

## Verify

`cd web && npx tsc -b && npx vitest run`
