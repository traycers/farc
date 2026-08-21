# Web: channel create/update/remove go through apid, not farcd directly

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

Switch the web app's channel write path from farcd directly to the new
`apid` (`issues/01-apid-server.md`), so every channel created/edited/
removed through the UI gets a matching mediamtx path automatically.

## Scope

- `web/src/api/farcd.ts`'s `createChannel`/`updateChannel`/`removeChannel`
  move to a new API module (e.g. `web/src/api/apid.ts`) pointed at
  `apid`'s routes instead of farcd's `/channels`. Request/response shapes
  follow `issues/01-apid-server.md`'s API surface (including surfacing
  the `{"farcd": "...", "mediamtx": "..."}`-style partial-failure
  response as a user-visible error, not a silent success).
- `ChannelNewPage.tsx`, `ChannelEditPage.tsx`, and the remove action on
  `ChannelsIndexPage.tsx` switch to these new calls.
- `listChannels`/`removeChannel`'s *read* counterpart and the WS journal
  subscription (`subscribeJournal`) are **not** touched — reads stay
  direct to farcd, per spec.md's explicit decision.
- **Both** of these need a new proxy path added for `apid`, and must be
  kept in sync with each other — `web/nginx.conf` (what Docker actually
  serves) and `web/vite.config.ts`'s dev proxy (what `vite dev` uses).
  CLAUDE.md already flags that these two have drifted before; call this
  out explicitly in the PR/review for this ticket.

## Comments
