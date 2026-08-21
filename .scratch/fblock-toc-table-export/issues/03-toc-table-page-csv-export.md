# 03 — Frontend: TOC table page (nav, filter, virtualization) + CSV export

Status: resolved

Depends on issue 02 (backend rows endpoints). Reuses issue 01's
`downloadTextFile` helper (`web/src/lib/download.ts`) — land after 01, or
duplicate the (trivial) helper if these need to ship independently.

## Task

New page showing TOC rows as a flat, filterable, virtualized table, reached
via a link from `FblockTreePage`, plus a CSV download. See spec decisions
3, 6-9, 12.

### API client (`web/src/api/fblockTree.ts`, extend — or a new sibling
module if it gets crowded)

1. `TocRow` type mirroring issue 02's `tocRow` JSON: `{id: number, type:
   string, role: string, parent_id: number, sibling_id: number,
   value_or_offset: string, size: number}` — `value_or_offset` is a
   **string** on the wire (`json:"value_or_offset,string"` on the Go side,
   issue 02's Comments), not a number: a `timestamp`/`duration` node's
   packed value is a unix-ns uint64 (~1.7e18), past JS's 2^53
   safe-integer limit. Treat it as an opaque string throughout — the
   table cell and `tocRowsToCsv` (step 9) just print it verbatim, no
   `Number()`/arithmetic. Same shape for both ready and live (spec
   decision 5's whole point). Mirror the existing `TreeNode`/`CatalogEntry`
   type style (`fblockTree.ts:7-15`, `21-28`).
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

2026-08-21: Implemented via TDD. Added `TocRow`/`TocRowsLiveMessage` +
`getFblockTocRows`/`subscribeFblockLiveTocRows` to `web/src/api/
fblockTree.ts` (mirroring `getFblockTree`/`subscribeFblockLiveTree`
exactly); `web/src/pages/fblockTocTablePaging.ts`
(`tocRowsToCsv`/`distinctValues`/`filterRows`, all pure, all
unit-tested); `web/src/pages/FblockTocTablePage.tsx` (new route
`/storages/:id/fblocks/:index/tree/toc` in `App.tsx`, next to
`FblockTreePage`'s), using `@tanstack/react-virtual`'s `useVirtualizer`
over the filtered row array — `filtered` is display-only, the CSV button
always serializes `rows` (the full fetch), never `filtered`.

Added the "TOC table" `<Link>` to `FblockTreePage.tsx`'s header row as its
own red-first cycle (`FblockTreePage.test.tsx`'s new "TOC table link"
describe block asserts the exact `href`) — this is the feature's headline
ask ("на странице фблока добавь переход на страницу отображения toc"), so
it got its own test rather than being folded into the new page's tests.

Testing `FblockTocTablePage` against a real `@tanstack/react-virtual`
hit jsdom's usual zero-size-container problem (the library measures the
scroll container's real layout, which jsdom always reports as 0, so it
would render 0 rows) — `FblockTocTablePage.test.tsx` mocks
`useVirtualizer` to return every row as a virtual item, testing this
page's own filter/CSV wiring rather than the library's viewport math. The
role/role-filter-option text collision (`"channel"` appears both as a
`<select>` option and a row cell) surfaced a genuine test-scoping bug on
the first run, fixed by scoping row assertions to a `data-testid="toc-rows"`
container via `within(...)`.

**Precision fix caught before writing any TS**: `internal/api`'s
`tocRow.ValueOrOffset` was originally a bare JSON number — a
`timestamp`/`duration` node's packed value (unix-ns, ~1.7e18) exceeds JS's
2^53 safe-integer limit and would have silently rounded in the browser.
Caught with a red-first Go test asserting the literal encoded JSON bytes
(a Go-side marshal/unmarshal round trip alone doesn't surface float64
precision loss); fixed with `json:"value_or_offset,string"`. `TocRow.
value_or_offset` is therefore `string` on the TS side from the start —
`tocRowsToCsv` never coerces it through `Number()`.

`cd web && npx vitest run` — 192 passed. `task build/web` — green
(`tsc -b` + `vite build`, same pre-existing >500kB chunk-size warning,
unrelated). `go build ./... && go vet ./... && go test ./...` and
`golangci-lint run` (whole repo) — all clean.
