# Web: PlayerPage gains a ?channel= query param with auto-submit

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

Let the Live page (`issues/04-web-live-page.md`) jump straight into
playing a specific channel's last hour of archive, in one click, instead
of landing on an empty search form.

## Scope

- `PlayerPage.tsx` reads a `channel` query param (`useSearchParams`, same
  pattern already used by `ChannelNewPage.tsx`'s `?storage=` param).
- When present: pre-check that channel id in `ChannelChecklist`'s
  `checked` state, and call the existing `onSearch` logic automatically
  on mount — do **not** require an extra manual click on "Search". The
  default `from`/`to` (`now − 1h → now`, already `PlayerPage`'s existing
  default — `ONE_HOUR_NS`) needs no change; it already satisfies "last
  hour" with no new code.
- No change to `PlayerPage`'s behavior when the query param is absent
  (manual channel-checkbox + form flow stays exactly as it is today).

## Comments
