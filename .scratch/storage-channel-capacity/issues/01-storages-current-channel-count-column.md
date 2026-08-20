# 01 — Storages page: current channel count column

Status: open

## Task

`StoragesIndexPage.tsx` shows `max channels` (`s.geometry.MaxChannels`,
header at line 55, cell at line 66) but never how many channels are
actually on a storage today. Add a `channels` column immediately to its
left.

1. The page currently only calls `listStorages()` (`StoragesIndexPage.tsx:31-35`).
   Also fetch `listChannels()` (from `../../api/farcd`, already used
   elsewhere, e.g. `ChannelsIndexPage.tsx`) and build a per-storage count —
   a single pass is enough, e.g.
   `channels.filter((c) => c.storage === s.id).length` per row, or a
   `Map<string, number>` built once if you'd rather not filter per row.
2. Add `<th>channels</th>` between `fblock size` (line 54) and
   `max channels` (line 55), and a matching `<td>` between the existing
   `fblock size`/`max channels` cells (lines 65-66).
3. Bump the empty-state `colSpan` from `6` to `7` (line 84).

## Tests

Vitest + RTL (no `StoragesIndexPage.test.tsx` exists yet — this is the
first). Mock `listStorages`/`listChannels`, assert:
- a storage with channels shows the right count in the new column, to the
  left of the existing `max channels` cell,
- a storage with zero channels shows `0`, not blank.

## Verify

`cd web && npx tsc -b && npx vitest run`
