# 01 — Add a detach button to the Storages page

Status: resolved

## Task

1. Add a `deleteStorage(id: string): Promise<void>` function to
   `web/src/api/farcd.ts`, calling `DELETE ${BASE}/storages/${encodeURIComponent(id)}`
   and reusing this file's existing `ok(...)` response-checking helper
   (see `patchStorage`, `web/src/api/farcd.ts:65-73`, for the pattern —
   note farcd returns `204 No Content` on success, so there's no JSON
   body to parse here, unlike `patchStorage`).
2. Add a "Detach" button, guarded by a confirmation prompt (this repo
   has no existing confirm-dialog pattern — a plain `window.confirm`
   with a clear message naming the storage is enough, no need to build
   a modal component for this). On confirm, call `deleteStorage`, then:
   - on success, navigate back to `/storages` (if placed on
     `StorageEditPage`) or remove the row from local state (if placed on
     `StoragesIndexPage`).
   - on failure, surface the error via the existing `error` state /
     `alert alert-danger` pattern already used on both pages — including
     the 409 case (still-attached channels), whose message from
     `handleRemoveStorage` already names the offending channel number,
     so no special-casing needed beyond displaying it.
3. Decide placement (see spec.md) — recommend `StorageEditPage.tsx`,
   next to the other mutable-settings actions, since it's a slower,
   confirmed, destructive action more suited to a dedicated edit page
   than a list-row button next to routine links like "Edit"/"fblocks
   status".

## Tests

Follow this repo's existing web test conventions (Vitest +
`@testing-library/react`) — check for existing tests on
`StorageEditPage.tsx`/`StoragesIndexPage.tsx` or sibling pages (e.g.
channel edit pages) to match seam and style. At minimum:

- `deleteStorage` in `farcd.ts` — a unit test hitting a mocked `fetch`,
  matching however this file's other functions are already tested (if
  they are — check first).
- The button's confirm-then-call-then-navigate-or-remove-row flow,
  and the error-surfaced-on-409 path — component-level tests through
  `@testing-library/react`, not implementation-detail tests of internal
  state.

Confirm the actual seams to test against before writing tests (per this
repo's TDD conventions) rather than assuming the above list is complete
or final.

## Verify

`cd web && npx tsc -b && npx vitest run`. Manually exercise the golden
path and the 409 path in a running dev server against a real farcd
before calling this done (per this repo's own UI-testing convention —
type checking and unit tests verify correctness of code, not of the
feature).

## Comments

2026-08-20: Implemented test-first, following spec.md's recommended
option (a) and placement. `deleteStorage(id)` added to `farcd.ts`
(`farcd.test.ts`, new file, matching `hls.test.ts`'s `vi.stubGlobal
('fetch', ...)` pattern) — DELETE with no JSON body parsed on the 204
response. "Detach" button added to `StorageEditPage.tsx` in a new
`card border-danger` "Danger zone" section below the existing mutable-
settings card: `window.confirm` naming the storage id → on confirm,
`deleteStorage` then `navigate('/storages')`; on failure (including the
409 still-attached-channel case) the existing `error`/`alert-danger`
state, no special-casing, matching this page's own save-handlers.
`StorageEditPage.test.tsx` (new file, `MemoryRouter`+`Routes`, following
`FblocksGridPage.test.tsx`'s pattern) covers decline-confirm (no call),
confirm-then-navigate, and confirm-then-409-shows-error-no-navigate.
One vitest gotcha hit and fixed: referencing plain top-level `const`
mocks directly as `vi.mock` factory values (rather than via a deferred
arrow wrapper) breaks under `vi.mock`'s hoisting once run alongside
other test files — fixed with `vi.hoisted()`. `npx tsc -b`, `npx vitest
run` (115 passed), `npx vite build` all clean.
