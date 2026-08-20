# Detach button on the Storages page

Status: resolved — grilled 2026-08-19 (surfaced incidentally during the
`remove-msm-integration` grilling session, unrelated to it), implemented
test-first 2026-08-20 (see `issues/01`'s Comments)

The web admin console's Storages page (`web/src/pages/storages/`) has no
way to remove a storage, even though farcd already exposes `DELETE
/storages/{id}` (`internal/api/storages.go`'s `handleRemoveStorage`).
This effort adds a "detach" button to the UI that calls it.

## Decisions (settled during grilling)

1. Not related to `remove-msm-integration` — that effort keeps `DELETE
   /storages/{id}` alive specifically because of this decision, but the
   two are independent pieces of work with no `Blocked by` relationship
   either direction.
2. Scope: one UI action, calling the existing farcd route. No new farcd
   API surface needed.

## Constraints to design around (from reading the existing handler)

`handleRemoveStorage` (`internal/api/storages.go`) refuses with **409**
if any channel is still attached to the storage — its own doc comment
currently frames this as a "last-resort guard," expecting the *real*
caller to remove every attached channel first rather than relying on
the 409. Since this button becomes a real, primary caller (not a
last-resort one) once it lands, decide explicitly whether the button:

- (a) simply calls `DELETE /storages/{id}` and surfaces a 409's error
  message as-is (e.g. "storage %q still has channel %d attached"),
  telling the operator to remove channels first themselves elsewhere in
  the UI, or
- (b) checks/blocks in the UI itself when the storage has attached
  channels (disabling the button, or requiring channel removal through
  the UI first).

Given every other mutation on this page (`onSaveName`,
`onSaveRetention`, `onSaveWriteMode` in `StorageEditPage.tsx`) follows
pattern (a) — call the API, catch, surface the error string via the
existing `error` state and `alert alert-danger` — (a) is the
consistent default unless the user prefers otherwise. This should be
confirmed as part of implementing this (or grilled further if it turns
out to need more nuance once actually built).

Also: farcd's removal is irreversible (destroys the storage's fblocks on
disk) — the UI should ask for confirmation before calling the route, not
fire it on a single click. This repo currently has no existing
confirm-dialog pattern anywhere in `web/src` (checked, none found) — the
implementer will need to introduce one (a plain `window.confirm`, or a
small modal, whichever fits the existing UI style better) rather than
following a precedent that doesn't exist yet.

## Where it goes

- Likely both `StoragesIndexPage.tsx` (as a button in the existing
  `btn-group btn-group-sm` alongside "Edit"/"fblocks status"/"fblocks
  list", `web/src/pages/storages/StoragesIndexPage.tsx:67-78`) and/or
  `StorageEditPage.tsx` (alongside the other mutable-settings actions,
  `web/src/pages/storages/StorageEditPage.tsx:124-179`) — decide during
  implementation which page(s) get it; a single page is probably enough,
  don't necessarily duplicate it on both without a reason.
- New API client function needed in `web/src/api/farcd.ts` (no
  `deleteStorage`/DELETE call exists there today — pattern it after
  `patchStorage`, `web/src/api/farcd.ts:65-73`).

## Issues

See `issues/01`.
