# 03 — Frontend: TOC table page (nav, filter, virtualization) + CSV export

Status: open

Depends on issue 02 (backend rows endpoints). Reuses issue 01's
`downloadTextFile` helper (`web/src/lib/download.ts`) — land after 01, or
duplicate the (trivial) helper if these need to ship independently.

## Task

New page showing TOC rows as a flat, filterable, virtualized table, reached
via a link from `FblockTreePage`, plus a CSV download. See spec decisions
3, 6-9, 12.

### API client (`web/src/api/fblockTree.ts`, extend — or a new sibling
module if it gets crowded)

1. `TocRow` type mirroring issue 02's `tocRow` JSON: `{id, type, role,
   parent_id, sibling_id, value_or_offset, size}` — same shape for both
   ready and live (spec decision 5's whole point). Mirror the existing
   `TreeNode`/`CatalogEntry` type style (`fblockTree.ts:7-15`, `21-28`).
2. `getFblockTocRows(storageId, uuid): Promise<TocRow[]>` — `fetch`
   against `${BASE}/storages/${storageId}/fcontainers/${uuid}/toc/rows`,
   mirroring `getFblockTree` (`fblockTree.ts:60-62`).
3. `subscribeFblockLiveTocRows(storageId, index, onMessage,
   onStatusChange?): () => void` — mirroring `subscribeFblockLiveTree`
   (`fblockTree.ts:78-121`) exactly, WS URL
   `.../storages/${storageId}/fblocks/${index}/toc/rows/ws`.

### New page (`web/src/pages/FblockTocTablePage.tsx`)

4. Route `/storages/:id/fblocks/:index/tree/toc`, registered in
   `App.tsx` next to `FblockTreePage`'s route (`App.tsx:62`), imported the
   same way (`App.tsx:11`'s import style).
5. **Add the nav link itself** — this is the feature's headline ask (spec
   decision 12: "переход на страницу отображения toc"), not just the
   destination page. In `FblockTreePage.tsx`'s header row
   (`FblockTreePage.tsx:58-65`), add `<Link
   to={`/storages/${id}/fblocks/${index}/tree/toc`}>TOC table</Link>`,
   styled like the existing tree links in
   `FblocksGridPage.tsx:140`/`FblocksListPage.tsx:99`. Without this step
   the new page is unreachable from the UI.
7. Same `getFblockInfo`/ready-vs-`in_progress` branching as
   `FblockTreePage.tsx:28-52` (reuse `getFblockInfo`, branch into
   `getFblockTocRows` for `ready` / `subscribeFblockLiveTocRows` for
   `in_progress`), and the same header badges
   (state/protected/uuid/begin/end, `FblockTreePage.tsx:67-78`) for
   context parity with the tree page.
8. Table columns, in order: `id, type, role, parent_id, sibling_id,
   value_or_offset, size` — no additional/derived columns (spec decision
   3; the table is an exact mirror of the CSV, not a display-optimized
   view).
9. Client-side search/filter: a text input plus `role`/`type` dropdowns
   populated from the distinct values actually present in the fetched
   `TocRow[]` — filtering only changes what's rendered, never the
   underlying fetched array (needed intact for the CSV export, decision
   9). No column sorting (decision 7 — native backend order is
   preserved).
10. Virtualization: add `@tanstack/react-virtual` to
    `web/package.json` (first table/list-virtualization dependency in this
    app — nothing comparable exists today) and use it to render only
    visible rows over the (post-filter) row array.

### CSV export

11. "Download TOC (.csv)" button. Serializes the full, **unfiltered**
    `TocRow[]` (decision 9) to tab-separated text with a header row:
    `id\ttype\trole\tparent_id\tsibling_id\tvalue_or_offset\tsize`, one row
    per line. Put the pure serialization in its own function (e.g.
    `tocRowsToCsv(rows: TocRow[]): string` in a new
    `web/src/pages/fblockTocTablePaging.ts` or similar), mirroring how
    `fblocksListPaging.ts` already keeps pure list logic separate from its
    page component — this keeps the CSV format unit-testable without
    rendering the page.
12. Reuse issue 01's `downloadTextFile(filename, content, mimeType)`
    (`web/src/lib/download.ts`), with `mimeType = 'text/csv'` and
    filename `fblock-${info.uuid ?? info.index}-toc.csv` (same uuid/index
    fallback convention as issue 01's txt filename).

## Tests

- `web/src/pages/fblockTocTablePaging.test.ts` (new): unit-test
  `tocRowsToCsv` — header row present, tab-separated, correct escaping-free
  raw numeric/string fields (no embedded tabs/newlines are possible in this
  data shape, since `value_or_offset` is always a raw number, never
  resolved string content — spec decision 3).
- `FblockTocTablePage.test.tsx` (new), modeled on `FblocksListPage.test.tsx`:
  renders rows for a `ready` fixture, exercises the role/type filter,
  asserts the CSV button always serializes the full unfiltered row set
  even while a filter is active (the regression this issue must guard
  against).

## Verify

`cd web && npm test`; `task build/web` (confirms the new dependency
resolves and the build stays green). Manually: open a `ready` fblock →
click the new "TOC table" link from the tree page → confirm row count
matches the tree's node count, apply a role filter, download CSV and
confirm it contains all rows regardless of the active filter. Repeat
against an `in_progress` fblock and confirm rows update via the live WS.

## Comments
