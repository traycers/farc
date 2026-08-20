# 01 — Player search button stays enabled with no channel selected

Status: open

## Task

1. In `web/src/pages/PlayerPage.tsx`, disable the "Search" submit button
   (`PlayerPage.tsx:121-123`) whenever `checked` (the selected-channels
   `Set`, from `ChannelChecklist`) is empty. Use the existing `disabled={...}`
   pattern already used elsewhere in this codebase for busy/paging states
   (e.g. `StorageNewPage.tsx:263`, `FblocksListPage.tsx:111,117`) rather than
   introducing a new pattern.
2. `onSearch` (`PlayerPage.tsx:75-91`) currently calls `getTimeline`
   unconditionally on submit — with the button disabled this path becomes
   unreachable via the UI, but leave the function itself as-is (no need for
   a redundant guard inside it, since the button being disabled prevents the
   form submit in the first place — confirm the `<form>` submit doesn't fire
   on a disabled submit button, standard HTML behavior, before assuming this
   is sufficient on its own).

## Tests

Follow this repo's existing web test conventions (Vitest +
`@testing-library/react`). Check for existing `PlayerPage` tests first to
match seam/style; at minimum, cover: button is disabled with an empty
checklist, enabled once at least one channel is checked, and clicking a
disabled button does not call `getTimeline`.

## Verify

`cd web && npx tsc -b && npx vitest run`. Manually exercise in a running dev
server: load the Player page with no channel selected (button disabled),
check one, button enables, uncheck it again, button disables again.

## Comments
