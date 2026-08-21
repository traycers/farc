# Live page: archive button becomes an icon, moves to the front of the row

Status: fixed (2026-08-21, via /mattpocock-skills:tdd)

See `.scratch/live-page-fixes/spec.md` for the full design conversation
this was split from.

## Goal

In `web/src/pages/LivePage.tsx`'s per-channel row (`:81-96`), turn the
"в архив" text button into an icon-only button and move it to the start of
the row, so the channel name — the only variable-width element — ends up
last, giving every row a stable fixed-width prefix.

## Scope

- Add the `bootstrap-icons` npm package to `web/package.json` (pairs with
  the already-used Bootstrap 5 dependency; import its CSS alongside the
  existing `import 'bootstrap/dist/css/bootstrap.min.css'` in
  `web/src/main.tsx`).
- New row order: icon button → checkbox → status indicator → name.
  Checkbox/indicator relative order is unchanged — only the archive link
  moves from the end to the start.
- Replace the `<Link>`'s text content ("в архив") with the `bi-clock-history`
  icon (`<i className="bi bi-clock-history" aria-hidden="true" />`), keep
  the same `to={`/player?channel=${c.channel}`}` navigation target and
  `btn btn-sm btn-outline-secondary` classes (drop `ms-auto`, no longer
  needed once the button isn't trailing).
- Add `title="Открыть в архиве"` and `aria-label="Открыть в архиве"` on the
  `<Link>` itself, since it no longer has visible text.
- `web/src/index.css`'s `.live-channel-row` flex layout needs no change
  (still `display:flex; align-items:center; gap:8px`) — reordering the JSX
  children is sufficient.

## Explicitly out of scope

- Any other row content or the checkbox/indicator's own markup — unchanged.
- `ChannelsIndexPage.tsx` is untouched by this issue (it has no equivalent
  "в архив" link).

## Comments
