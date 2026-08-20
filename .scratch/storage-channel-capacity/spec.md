# Storage channel capacity — current count column + blocking full storages

Status: resolved — grilled 2026-08-20, implemented test-first 2026-08-20 (see each issue's Comments)

Two requests filed together because they hinge on the same underlying data
point: "how many channels currently point at this storage" (`GET /channels`
grouped by `storage`). No other shared code path.

## Decisions (settled during grilling)

1. `StoragesIndexPage.tsx` gets a new `channels` column, immediately left of
   the existing `max channels` column (`s.geometry.MaxChannels`,
   `StoragesIndexPage.tsx:55/66`), showing
   `channels.filter(c => c.storage === s.id).length` from `GET /channels`.
2. "Full" = `count(channels currently on a storage) >= geometry.MaxChannels`,
   computed from the same `GET /channels` list every page already fetches.
   This is a deliberately simple, accepted divergence from the backend's own
   lazy fblock-catalog registry check
   (`internal/index/channels.go:88-98`, `ErrChannelRegistryFull`), which
   counts *distinct channels that have ever written into a storage's live
   catalog* — a different, delayed, and not-easily-decreasing number. This
   issue's notion of "full" is not that; it's simply today's configured
   channel roster vs. `MaxChannels`, and is not meant to replace or fix the
   registry check.
3. Enforcement isn't UI-only: `POST /channels` and `PUT /channels/{id}`
   (when the update changes a channel's storage) both reject with
   `409 Conflict` when the destination storage's current channel count
   already meets `MaxChannels`. Editing a channel *without* changing its
   storage never triggers the check — the channel already occupies its slot
   there, so re-validating capacity against itself would be nonsensical
   without tracking "is this the channel being edited," which the simpler
   same-storage-vs-different-storage rule avoids entirely.
4. Frontend, three places:
   - `ChannelsIndexPage.tsx`: the "New channel" `Link` becomes a disabled,
     non-navigating `<button>` when the storage currently selected in this
     page's own filter is full, with a `title` explaining why (e.g. "Storage
     full (8/8 channels)").
   - `ChannelNewPage.tsx` / `ChannelEditPage.tsx`: full storages are
     `disabled` `<option>`s in the storage `<select>`, with the option text
     itself noting fullness (e.g. "Storage One (full, 8/8)") since
     `<option title>` tooltips aren't reliably supported cross-browser. The
     submit button is also disabled whenever the currently selected storage
     is full (covers a full storage arriving pre-selected, e.g. via the
     `?storage=` query param from `.scratch/channels-new-default-storage`).
   - `ChannelEditPage.tsx` never disables or blocks-on-submit the channel's
     own *original* storage, regardless of its fullness (decision 3's
     same-storage exception, mirrored client-side).
5. Filed as two issues: 01 (count column, pure frontend) and 02 (block full
   storages, frontend + backend). 01 isn't a hard code dependency for 02 —
   each page fetches `listChannels()`/`listStorages()` independently — but
   both compute the exact same "count per storage" value, so implement 01
   first and reuse its approach.

## Issues

- `issues/01` — Storages page: current channel count column
- `issues/02` — Block channel creation/move into a full storage (frontend + backend)
