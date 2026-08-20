# 01 — Pass selected storage from Channels to New Channel via query param

Status: open

## Task

`ChannelsIndexPage.tsx`'s "New channel" link (`ChannelsIndexPage.tsx:115-117`,
`<Link to="new">`) doesn't carry the page's currently-selected storage
filter (`storage` state, line 17) to `ChannelNewPage.tsx`, which always
defaults its own `storage` state to `listStorages()`'s first entry
(`ChannelNewPage.tsx:28-38`).

1. `ChannelsIndexPage.tsx`: change the link to
   `<Link to={`new?storage=${encodeURIComponent(storage)}`}>`.
2. `ChannelNewPage.tsx`: import `useSearchParams` from `react-router-dom`
   and read the `storage` param. Seed the `storage` state
   (`useState('')`, line 18) from it instead of `''`. Once `listStorages()`
   resolves (lines 28-38), only keep the param's value if it's among the
   fetched storages — otherwise fall back to today's `s[0].id` default (a
   stale/removed storage id in the URL shouldn't silently create a broken
   selection).

## Tests

Vitest + RTL, extending the `ChannelNewPage.test.tsx` pattern added this
session:
- render with `<MemoryRouter initialEntries={['/channels/new?storage=s2']}>`
  and mocked `listStorages` resolving `[s1, s2]` — assert the storage
  `<select>` initializes to `s2`.
- same setup with `?storage=unknown` (or no query param at all) — assert it
  falls back to `s1` (the first fetched storage).

## Verify

`cd web && npx tsc -b && npx vitest run`
