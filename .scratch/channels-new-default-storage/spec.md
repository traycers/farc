# Channels page: carry selected storage into "New channel"

Status: resolved — grilled 2026-08-20, implemented test-first 2026-08-20 (see issue's Comments)

## Decisions (settled during grilling)

1. `ChannelsIndexPage.tsx`'s "New channel" link (`Link to="new"`, lines
   115-117) currently drops the page's own storage filter (`storage` state,
   line 17) entirely — `ChannelNewPage.tsx` always defaults to the first
   entry from `listStorages()` (lines 28-38,
   `setStorage((cur) => cur || (s.length > 0 ? s[0].id : ''))`) regardless
   of what was selected on the Channels page.
2. Fixed via a query parameter, not router state: a query param survives a
   page reload / direct link, and `web/src` doesn't have any concept of
   passing router `state` today either — this is the first case of either,
   and a query param is the more conventional, bookmarkable choice.
3. `ChannelNewPage.tsx` falls back to the existing "first storage" default
   whenever the query param is absent, or names a storage id that isn't in
   the fetched `listStorages()` result (stale/removed storage) — no new
   error state for this, it silently degrades to today's behavior.

## Issues

- `issues/01` — Pass selected storage from Channels to New Channel via query param
