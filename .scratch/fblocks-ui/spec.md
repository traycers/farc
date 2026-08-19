# fblocks admin UI: rename, per-fblock tree, list page, pool-status plan

Settled via `/mattpocock-skills:grilling`, 2026-08-14 (one question at a
time, per user preference). Source request (user, verbatim intent):

1. Live-tree page is not needed (tree too big/unwieldy merged across a
   whole storage).
2. New "fblocks list" page: storage selector + table of fblocks, each row
   with a button to a "fblock tree" page showing that one fblock's tree,
   collapsible nodes, default expanded to depth 5.
3. Rename `storages/{id}/fblocks` (the grid/squares page) to "fblocks
   status".
4. Additionally (**plan only, not to be implemented this round**): a live
   per-pool-fblock fullness-status list above the existing squares on
   "fblocks status", one row per pool slot: `[status square][prolog]
   [catalog][content][toc][epilog]`, sized proportionally to actual bytes,
   showing which fblock is writing/queued/filling/free.

## Key facts discovered during grilling (research, not decisions)

- `storages/{id}/fblocks` (grid) already links `ready`-state squares to
  `FblockStatusPage` (`/storages/:id/fblocks/:index/status`), which
  already renders a fcontainer's TOC as a tree via the shared
  `FblockTree` component — **the "fblock tree" page the user asked for
  already exists in seed form**; it needs live (`in_progress`) support +
  collapse/expand + depth-5 default, not a parallel new page.
- The live-tree page's backend (`handleStorageLiveTreeWS`,
  `mergeLiveTrees`, `remapTreeIDs`) merges each channel's *separately
  numbered* live tree into one combined view. That was only ever needed
  because, at the time it was written, each channel had its own private
  `Filler`. Since `.scratch/multi-channel-fcontainer/issues/02` (resolved
  2026-08-14, same day), every channel of a Storage now shares one
  `*ingest.StorageSegment` — so a single already-coherent `root ->
  channels -> channel(xN)` tree is available directly from
  `StorageSegment.Elements()`. `mergeLiveTrees`/`remapTreeIDs` are exactly
  the "known follow-up" ticket 02's own Comments section flagged as
  no-longer-needed scaffolding once this existed — this effort retires
  them.
- `GET /storages/{id}/catalog` and `GET .../fblocks/{index}` do **not**
  report byte size at all today (only index/state/uuid/begin/end/
  protected). Needed for item 4's plan; not needed for item 2's table
  (user explicitly said size is redundant there — fixed per storage).
- `internal/storage/pool.go`'s `Pool` has no enumeration accessor, only
  the aggregate `Status()` — item 4 (whenever it's built) needs a new
  exported way to list occupied slots' state.
- TOC's encoded byte size is a **pure, exact, closed-form function of
  element count alone** (128-byte header/directory + 27 bytes/element +
  <=418 bytes bounded alignment padding across 6 SoA columns) — confirmed
  by reading `toc/build.go`/`toc/columns.go`. No sorting/dedup/whole-tree
  knowledge affects the byte count. This means item 4's live TOC-size
  figure can be exact, not an estimate, from a running element count —
  no `toc.Build` call needed while filling.

## Decisions (settled, one question at a time)

1. **fblock-tree scope**: covers both closed (`ready`, static TOC) and
   currently-writing (`in_progress`, live) fblocks.
2. **Frame-node grouping** (to keep the tree from being enormous):
   done entirely client-side (web), never touching farcd. Runs of
   sibling frame-role nodes group into a single collapsed, clickable
   summary row ("frames 42-141, 100 total"), click expands back to
   individual frames. Threshold: **100 for video, 500 for audio**.
3. **Default expand depth**: root = depth 0; levels 0-4 (5 levels) open
   by default. Plus explicit "expand all"/"collapse all" controls (click-
   to-toggle-per-node alone isn't enough on a tree this size).
4. **Grid-square clickability**: `ready` and `in_progress` squares both
   link to the (extended) fblock-tree page; `bad`/`uninitialized` stay
   non-interactive (nothing to show).
5. **`fblocks list` columns**: index, state (+protected), begin/end.
   Explicitly **no uuid, no size** (size is identical for every fblock in
   a storage — redundant in a per-row table).
6. **`fblocks list` scale handling**: pagination, implemented **purely
   client-side** (the catalog is already fetched in one cheap request, as
   today) — no server-side `limit`/`offset`. Virtualization on top of
   that is not needed once paginated (page size already bounds rendered
   rows). Plus a simple state filter (e.g. hide `uninitialized`).
7. **Navigation**: "fblocks list" and "fblocks status" (renamed grid) get
   separate top-level nav entries — different tasks (search/inspect vs.
   glance-overview), not tabs of one page. The old live-tree nav entry is
   removed outright.
8. **Old live-tree backend**: `handleStorageLiveTreeWS` and its supporting
   merge/remap types are deleted entirely — nothing else consumes them,
   and the new per-fblock live endpoint (built directly on the shared
   `StorageSegment`) replaces their purpose more simply.
9. **Pool-status-list states** (item 4, plan only): four states — *free*
   (slot not reserved), *filling: queued* (reserved, accumulating, no
   physical index yet), *filling: active* (reserved, has physical index,
   really `in_progress`), *closing* (final TOC+epilogue write, incl.
   possible corruption-retry).
10. **Pool-status-list row count** (item 4): always exactly
    `PoolTuning.Size` rows, including free slots — shown as empty/
    translucent rows at their expected/default sizes (not omitted, not a
    visually distinct "cut-down" shape).
11. **Pool-status-list section sizing** (item 4): the 5 section boxes
    (prolog/catalog/content/toc/epilog) are drawn proportionally to their
    actual byte size (stacked-bar), not fixed-width-with-text-label.
12. **Pool-status-list TOC size** (item 4): computed **exactly** (not
    estimated) via the closed-form element-count formula above — no
    `toc.Build` call during fill.
13. **Pool-status-list live transport** (item 4): reuses the existing
    per-storage WS event feed (`internal/api/eventpush.go`) with new
    event types layered on — no separate polling mechanism.

## Scope split

- `issues/01` — rename grid page + clickable `in_progress` squares
  (small, no blockers).
- `issues/02` — the fblock-tree page itself: remove old live-tree
  page+backend, add new per-fblock live endpoint (on top of the shared
  `StorageSegment`, retiring `mergeLiveTrees`/`remapTreeIDs`), collapse/
  depth-5/expand-all, client-side frame grouping. Largest ticket.
- `issues/03` — new "fblocks list" page (table, client pagination+filter,
  nav entry). Independent of 02 (only needs 01's nav pattern).
- `issues/04` — **plan only**: the pool-status-list design for a future
  ticket. Not scheduled for implementation this round.
