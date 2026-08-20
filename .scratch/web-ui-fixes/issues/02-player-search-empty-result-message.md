# 02 — Player search shows nothing when zero fragments match

Status: resolved

## Task

`computeDataRange` (`web/src/pages/playerTimeline.ts:51-62`) already computes
the earliest/latest segment across all selected channels and sets
`playheadNs` to the range start (`PlayerPage.tsx:87`) — this is the "jump to
first available fragment" behavior and already works. The actual gap: when
`getTimeline` returns zero segments for every selected channel,
`computeDataRange` silently falls back to the requested `[t1, t2]` and the
playhead moves there anyway, with no indication that nothing was actually
found.

1. In `onSearch` (`PlayerPage.tsx:75-91`), detect the zero-segments case (no
   segment across any selected channel within `[t1, t2]`) and set the
   existing `error` state to a message such as "No records found in the
   selected range" instead of proceeding as if data was found. Recommend the
   playhead should *not* move in this case (nothing to show) — confirm this
   during implementation.
2. Reuse the existing inline-alert pattern already on this page
   (`PlayerPage.tsx:128`, `{error && <div className="alert alert-danger">{error}</div>}`)
   — no new UI primitive needed.

## Tests

Vitest + `@testing-library/react`, matching this page's existing test seam
if one exists. Cover: search with a range containing no segments for any
selected channel shows the message and does not move the playhead; search
with at least one segment behaves as today (no message, playhead moves to
earliest segment start).

## Verify

`cd web && npx tsc -b && npx vitest run`. Manually verify against a real
farcd: search a channel/time range known to have no recorded data, confirm
the message appears and the playback area doesn't silently show an
empty/broken state.

## Comments

2026-08-20: Implemented test-first — new `hasAnySegments` pure helper in
`playerTimeline.ts` (`playerTimeline.test.ts`), wired into `onSearch`
(`PlayerPage.tsx`) to show "No records found in the selected range" and
leave the playhead/timelines unset instead of falling back to the raw
search window. Playhead deliberately does not move in this case (decided
during implementation, per the issue's own recommendation).
