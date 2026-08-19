# Rename fblocks grid page to "fblocks status"; make in_progress squares clickable

Status: resolved

## Problem

`web/src/pages/FblocksGridPage.tsx` (route `/storages/:id/fblocks`) is the
squares/grid overview page. It needs a clearer name ("fblocks status") and,
now that `issues/02` gives `in_progress` fblocks a live tree view too, its
`in_progress` squares should become clickable the same way `ready` squares
already are.

## Design (settled, see `../spec.md`)

- Page title/nav label becomes "fblocks status"; route path can stay
  `/storages/:id/fblocks` (URL isn't part of what the user asked to
  rename — only the human-facing label).
- `entry.state === 'ready'` check (FblocksGridPage.tsx:110) becomes
  `entry.state === 'ready' || entry.state === 'in_progress'` for the
  `<Link>` branch.
- `bad`/`uninitialized` stay non-interactive `<div>`s — nothing to show.
- Target of the link is whatever route `issues/02` lands the fblock-tree
  page on (today `.../fblocks/{index}/status`; `issues/02` renames this
  to `.../fblocks/{index}/tree` to avoid the "status" vs. "fblocks
  status" naming collision — see that ticket).

## Scope (for /plan)

- Depends on `issues/02`'s route rename landing first (or land both in
  the same PR) so the Link target is correct on the first try.
- No backend changes needed — `entry.state` is already in the existing
  `GET /storages/{id}/catalog` response.
- Test: existing `FblocksGridPage` tests (if any) updated to assert
  `in_progress` squares render as links; nav label test if one exists.

## Comments

Implemented (2026-08-14): `FblocksGridPage.tsx` heading -> "Fblocks status",
`in_progress` added to the clickable-square condition alongside `ready`,
link target updated to `.../fblocks/{index}/tree` (ticket 02's renamed
route). `StoragesIndexPage.tsx`'s "fblocks" button relabeled "fblocks
status". New test `FblocksGridPage.test.tsx` (vitest, first use of the
newly-added vitest+testing-library setup in this repo) covers all four
square states.
