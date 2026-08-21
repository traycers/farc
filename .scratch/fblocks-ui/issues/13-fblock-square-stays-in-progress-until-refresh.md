# First fblock square stays in_progress (blue) until a manual page refresh

Status: fixed (2026-08-21, via TDD)

## Problem

Reported 2026-08-21: on the Fblocks status page, after the first fblock
finished writing, its square stayed blue (`state-in_progress`) instead of
turning green (`state-ready`) — until the page was manually refreshed,
which fetches a fresh catalog over REST and shows the correct color.

## Root cause

A race between `FblocksGridPage.tsx`'s initial `getCatalog(id)` REST call
and its `subscribeStorageEvents(id, ...)` WS subscription, both fired from
the same `useEffect`. The client's WS connection opens immediately, but
the server only starts forwarding events for storage `id` after it
receives the client's own subscribe JSON message
(`internal/api/eventpush.go`'s `conn.ReadJSON(&sub)` → `unit.Notify().Subscribe(64)`).
Between the WS opening and that handshake completing, any
`fblock.write.completed` event is silently lost — this protocol has no
reconnect/catch-up (a documented limitation elsewhere, but here it bites
on the *first* connection, not a reconnect). If the first fblock finishes
writing quickly enough to land in that window, the square is stuck at its
last REST-fetched state (`in_progress`) until something else re-fetches —
a manual page reload being the only thing that does.

## Fix

Frontend-only, does not touch the WS protocol (a fuller fix would have
the client send its subscribe target as part of the WS upgrade itself,
e.g. a query param, so the server subscribes before any message round
trip — deferred, this is the "minimal" option the user chose): once
`subscribeStorageEvents`'s `onStatusChange` callback reports `connected`,
`FblocksGridPage.tsx` does one additional `getCatalog(id)` re-fetch. This
doesn't make the race impossible in principle (there's still no explicit
ack that the server has *finished* processing the subscribe message by
the time `connected` fires), but it closes the window down to roughly one
WS round trip instead of "however long the initial REST fetch + WS
connect took", which is what actually manifested the bug in practice.

## Comments

Diagnosed by an agent fork reading `FblocksGridPage.tsx` and
`internal/api/eventpush.go` side by side — no live reproduction needed,
the race was visible directly in the code's control flow. Covered by a
new `FblocksGridPage.test.tsx` case asserting `getCatalog` is called a
second time after the mocked `subscribeStorageEvents`'s status callback
fires `true`, and that the rendered square picks up the second fetch's
state. Verified: `npm test` (171/171 green), `npm run build` clean,
rebuilt and redeployed to the reporting user's live `docker compose`
stack (`docker compose up -d --build web`).
