# TOC table view + txt/CSV export for the fblock tree page

Status: open — grilled 2026-08-21

From the fblock tree page (`FblockTreePage`, `/storages/:id/fblocks/:index/tree`)
add a link to a new page that shows the same TOC data as a flat table instead
of a tree, plus two downloads: the fully-expanded tree as `.txt`, and the TOC
table as tab-separated `.csv`. Filed as one directory because all three
pieces share the same underlying TOC/live-tree data model and two of them
(table, CSV) are the same page.

## Decisions (settled during grilling)

1. **Availability.** Both the table/CSV and the txt-tree download work for
   `ready` fblocks (real on-disk TOC) *and* `in_progress` fblocks (the live
   tree the page already streams over WS). There is no "TOC-only-when-ready"
   restriction — an in_progress fblock gets an equivalent live projection
   into the same row shape (decision 5).
2. **No client-side flattening of the nested tree.** TOC *is* already a
   Structure-of-Arrays on disk (`toc.Columns`, `toc/columns.go:35-48` —
   `Type`/`Role`/`Parent`/`Sibling`/`ValueOrOffset`/`Size`, literally
   parallel slices) and `unit.ReadTOC` (`internal/storage/reader.go:82-101`)
   already returns it in that shape *before* the existing
   `handleReadFblockTree` handler (`internal/api/fblocktree.go:120-131`)
   nests it into `TreeNode` JSON via `buildColumnsTree`/`columnsNode`
   (`fblocktree.go:94-116`). Turning that nested JSON back into rows on the
   client would be undoing work the backend already has to *not* do — the
   table gets its own backend endpoint that projects `toc.Columns` straight
   into rows, skipping the tree-building walk entirely (issue 02).
3. **Columns — raw SoA, no decoration.** `id, type, role, parent_id,
   sibling_id, value_or_offset, size`. `type`/`role` are the existing
   name-strings (`mediatree.NodeType.String()`/`Role.String()`,
   `mediatree/type.go:58-87`, `mediatree/role.go:81-86`) — not the on-disk
   numeric codes, since numeric codes are unreadable without a lookup table
   and the current tree API already exposes names. `value_or_offset` is the
   *raw* number: for fixed-width types this is the packed inline value
   (`toc/build.go:98-104`'s `packInline` convention), for `bytes`/`string`
   types it is the raw Content byte offset — **not** the resolved
   string/bytes payload (Content is never read for this feature, same as
   today's tree endpoint, which only ever exposes `Size` for variable-width
   nodes, never the bytes). No derived `depth`/path column — the table is
   an exact mirror of the CSV export, nothing added for on-screen
   readability alone.
   - JSON/CSV field names are `parent_id`/`sibling_id`, not
     `parent`/`sibling` — matching the existing `TreeNode.ParentID` →
     `"parent_id"` convention (`fblocktree.go:34`) rather than
     `toc.Columns`'s Go field names, so the two row-shaped APIs
     (`TreeNode`-tree and TOC-rows) read consistently.
4. **`sibling` sentinel.** First child of a parent self-references:
   `sibling_id == id`, exactly like `parent_id == id` for the root
   (`mediatree/node.go:19`'s doc comment, enforced in `toc/build.go:63-69`,
   asserted in `mediatree/validate.go:49`). Reuse this convention for the
   live-row projection too — don't invent a second sentinel.
5. **Live (`in_progress`) rows.** No on-disk TOC exists yet, so the backend
   computes the same six columns from the in-memory tree
   (`mediatree.Element` slice from `IngestManager.LiveTreeForStorage`,
   `internal/ingest/ingestmanager.go:288-296`) instead of reading `toc.Columns`
   off disk:
   - `parent_id`/`sibling_id` come straight off `Element.Parent`/`.Sibling`
     (`mediatree/node.go:15-21`) — `Filler.append`
     (`internal/fcontainer/filler.go:70-79`) already maintains the same
     self-reference-sentinel convention as decision 4, and rows are already
     in creation order with `Parent[i] <= i` guaranteed, so **no DFS reorder
     is needed** for the live path (unlike the ready path, where
     `toc.Build`'s reorder is what produces the row ids in the first
     place).
   - `value_or_offset` for variable-width (`bytes`/`string`) types is **0**
     in live rows. `mediatree.Element.Value` already holds the fully
     decoded bytes in memory (no Content-offset indirection exists
     pre-finalization — confirmed at `fblocktree.go:176-180`'s comment), so
     there is no "raw offset" to report; this matches today's live tree,
     which likewise never exposes a byte offset for these nodes.
   - For fixed-width types, live rows still need a real packed numeric
     value so live and ready rows are bit-identical for the same logical
     node — reuse (exporting if necessary) `toc/build.go`'s
     `packInline`-style bit-packing on `Element.Value` rather than
     inventing a second packing scheme.
6. **id spaces are not synchronized across the `in_progress` → `ready`
   transition.** Ready-row ids are post-DFS-reorder positions
   (`toc/columns.go:33-35`); live-row ids are Content creation-order
   positions. The same logical node can have a different `id` before and
   after finalization. This is an accepted, documented discrepancy — no
   attempt is made to keep ids stable across that boundary.
7. **Row order.** Native order as returned by the backend (DFS-preorder for
   ready, creation order for live) — no client-side sorting.
8. **Scale.** TOC rows can run into the thousands (busy fblock, many frame
   nodes). The table ships from day one with a client-side search/filter by
   `role`/`type` and row virtualization, using a new dependency,
   `@tanstack/react-virtual` — `web/package.json` currently has no
   table/virtualization library at all (only `bootstrap`,
   `react-router-dom`, `hls.js`).
9. **CSV always exports the full, unfiltered TOC**, tab-separated, header
   row included — independent of whatever role/type filter is currently
   applied in the table UI. The table is a browsing aid; the CSV is a full
   dump.
10. **txt tree export is the *raw* tree, not the on-screen display tree.**
    `FblockTreePage` renders `groupFrameNodes(tree)`
    (`FblockTreePage.tsx:54`), which collapses long runs of same-role frame
    siblings (≥100 video / ≥500 audio, `frameGrouping.ts:8-11`) into a
    synthetic `type: "group"` node that doesn't exist in the real TOC/live
    tree (`frameGrouping.ts:19-56`, negative synthetic ids). The txt export
    serializes the un-grouped `tree` state instead, so every line in the
    file corresponds to a real TOC/live node — consistent with decision 3's
    "no client-side decoration" principle applied to the table.
11. **txt format.** An exact ASCII-art copy of what `FblockTree.tsx` renders
    on screen (`├──`/`└──`/`│` connectors, `FblockTree.tsx:65-66`; decoded
    values via `formatValue`, `FblockTreePage.tsx:9-13` +
    `fblockTreeFormat.ts:16`), but with every node forced open — independent
    of the page's current collapse/expand UI state
    (`mode`/`manual`, `FblockTree.tsx:111-112`). No such plain-string
    serializer exists today (`TreeLines`, `FblockTree.tsx:42-100`, is
    JSX-only and tightly coupled to React state) — this needs new,
    React-free tree-walking code.
12. **Navigation and button placement.**
    - A new page, not a tab/mode toggle on `FblockTreePage` — the request
      explicitly asks for a *page* ("страница отображения toc"), and
      `FblockTreePage` already juggles two data sources (static/live); a
      second page keeps that logic from doubling up on itself.
    - Route: `/storages/:id/fblocks/:index/tree/toc`, new
      `FblockTocTablePage.tsx`, registered next to
      `FblockTreePage`'s route (`App.tsx:62`).
    - Nav link to it lives in `FblockTreePage`'s header row
      (`FblockTreePage.tsx:58-65`), matching the `Link`-construction style
      already used by `FblocksGridPage.tsx:140` /
      `FblocksListPage.tsx:99`.
    - "Download tree (.txt)" button lives on `FblockTreePage` (it needs the
      raw `tree` state, which only that page holds — `FblockTree.tsx` only
      ever receives the already-grouped `displayTree`), enabled whenever
      `tree` is non-null, working for both `ready` and `in_progress`.
    - "Download TOC (.csv)" button lives on the new
      `FblockTocTablePage`.

## Issues

- `issues/01` — Download expanded tree as `.txt` (frontend only, no backend
  dependency — can land first)
- `issues/02` — Backend: expose TOC as flat SoA rows, for `ready` (on-disk)
  and `in_progress` (live) fblocks
- `issues/03` — Frontend: TOC table page (nav link, filter, virtualization)
  + CSV export (depends on `02`; reuses `01`'s file-download helper)
