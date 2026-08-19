# New "fblocks list" page: storage selector + fblocks table

Status: resolved

## Problem

There's no tabular/searchable view of a storage's fblocks today — only
the visual grid (`FblocksGridPage`, being renamed to "fblocks status" in
`issues/01`). A table is better suited to scanning/filtering many fblocks
by state or time range than a wall of squares.

## Design (settled via grilling, see `../spec.md`)

- New page, new top-level nav entry (not a tab inside "fblocks status" —
  different tasks: glance-overview vs. search/inspect).
- Storage selector (same pattern as the existing grid page's `<select>`,
  `FblocksGridPage.tsx:95-104`).
- Table columns: **index, state (+protected badge), begin/end**.
  Explicitly no uuid column, no size column (size is identical for every
  fblock in a storage — confirmed redundant per-row).
- Each row has a button/link to the fblock-tree page (`issues/02`'s
  `.../fblocks/{index}/tree`).
- Pagination: **client-side only** — `GET /storages/{id}/catalog` keeps
  returning the whole catalog in one request (already cheap, per that
  handler's existing doc comment), the table just paginates what's
  already fetched. No new `limit`/`offset` backend params.
- Simple state filter (e.g. a `<select>` or checkboxes to hide
  `uninitialized`).
- Virtualization not needed once paginated (page size already bounds
  rendered rows).

## Scope (for /plan)

- No backend changes at all — `getCatalog` (`web/src/api/fblockTree.ts`)
  already returns everything this page needs.
- Page size default (e.g. 50/page) — not specified by the user, pick a
  reasonable default during implementation.
- New route (e.g. `/storages/:id/fblocks-list`) and nav entry, added
  alongside `issues/01`'s renamed "fblocks status" entry in `App.tsx`.
- Test seams: pure pagination/filter logic (if extracted as a helper) at
  the component/hook level; component test asserting the button navigates
  to the right `.../tree` URL.

## Comments

Implemented (2026-08-14): new `web/src/pages/FblocksListPage.tsx` (route
`/storages/:id/fblocks-list`, new top-level nav entry on
`StoragesIndexPage.tsx`), table columns index/state(+protected)/begin/end
(no uuid, no size). Pure pagination/filter logic extracted to
`fblocksListPaging.ts` (`visibleEntries`/`pageOf`/`totalPages`, TDD'd with
vitest) — client-side only, no backend changes (existing `getCatalog`
unchanged). Default: hide `uninitialized`, togglable checkbox; 50 rows/page.
Row button links to ticket 02's `.../fblocks/{index}/tree`.
