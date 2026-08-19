# player: timeline time-axis labels + range clipped to actual recordings

Status: fixed (2026-08-18, via `/mattpocock-skills:tdd`)

See `.scratch/player-redesign/spec.md` for the full original design this
extends (issues 01/02 already shipped 2026-08-18).

## Problem

- `TimelineBar.tsx` renders channel rows + one shared cursor and nothing
  else — no clock-time labels/ticks anywhere along the bar, so a user
  can't tell roughly when a rectangle starts without clicking it and
  reading the separate "текущее время" line above.
- The timeline's `[rangeStart, rangeEnd]` is exactly the raw search-form
  `[t1, t2]` (minute-rounded by the `datetime-local` inputs), not clipped
  to where actual recordings are — if the search window is much wider
  than the actual data, most of the bar is empty space.

## Facts gathered during grilling

- Confirmed the timeline already uses true absolute unix-ns end to end
  (`segmentToRect`/`fractionToNs`, `Segment.begin`/`end` as bigints) — no
  relative-time representation exists anywhere; the perceived "is it
  absolute?" question was purely about the missing labels, not the
  underlying values.
- `PlayerPage.tsx`'s `onSearch` sets `rangeStart = t1`, `rangeEnd = t2`,
  `playheadNs = t1` unconditionally, with no clipping to `getTimeline`'s
  actual result.
- `TimelineBar.tsx` is 47 lines total: outer `.player-timeline` click
  handler, one `.player-timeline-row` per channel with `.player-
  timeline-segment` spans, and one shared `.player-timeline-cursor` span.
  No ticks/ruler/labels exist in either the component or `index.css`.

## Design decisions (grilling, 2026-08-18)

- **Range**: `rangeStart`/`rangeEnd` computed from the actual returned
  segments across every checked channel — `min(begin)` floored to the
  whole second, `max(end)` ceiled to the whole second. Falls back to the
  raw `t1`/`t2` search values if no segments are returned at all.
- **Playhead init**: `playheadNs` on search is set to the new
  `rangeStart` (not the raw `t1`), so it always starts inside the drawn
  range.
- **Axis algorithm**: new pure module `web/src/pages/timelineTicks.ts` (+
  `.test.ts`) — a "nice tick" algorithm (step chosen from a 1/2/5/10/15/30
  sequence across seconds/minutes/hours) such that adjacent labels are at
  least ~80px apart given a supplied `widthPx`. Roughly:
  `computeTicks(rangeStart, rangeEnd, widthPx): {ns: bigint, label:
  string, leftPct: number}[]`.
- **Width source**: measured once via `ref.getBoundingClientRect().width`
  in an effect after `timelines`/`rangeStart`/`rangeEnd` change — no
  `ResizeObserver`; the page isn't expected to be resized mid-session.
- **Label format**: `HH:MM` when the chosen step is ≥ 60s, `HH:MM:SS`
  otherwise. Multi-day ranges are explicitly not handled specially.
- **Placement**: one shared axis row below all channel rows (not
  per-channel), non-clickable — click-to-seek stays on the existing
  segments/cursor area only.
- No Plan Mode — straight to `/mattpocock-skills:tdd`; no new e2e
  scenario (existing 4 Playwright specs re-run as regression only, since
  the new logic is fully covered by unit tests on pure functions).

## Fix

- `web/src/pages/playerTimeline.ts`: new `computeDataRange(timelines,
  fallbackStart, fallbackEnd)` — min `begin`/max `end` across every
  channel's segments, floored/ceiled to the whole second
  (`floorToSecond`/`ceilToSecond` helpers), falling back to
  `fallbackStart`/`fallbackEnd` when no segments exist.
- `web/src/pages/PlayerPage.tsx`'s `onSearch`: now calls
  `computeDataRange(result, t1, t2)` and sets `rangeStart`/`rangeEnd`/
  `playheadNs` from its `{start, end}` instead of the raw `t1`/`t2`.
- `web/src/pages/timelineTicks.ts` (new): `computeTicks(rangeStart,
  rangeEnd, widthPx)` — a "nice tick" algorithm. `NICE_STEPS_SECONDS`
  (1/2/5/10/15/30 across seconds/minutes/hours, up to 24h, whole-day
  multiples beyond that) picks the smallest step keeping labels
  `MIN_PX_PER_TICK` (80px) apart given `maxLabels = max(2,
  floor(widthPx/80))`; ticks land on that step's absolute-time grid
  (`ceilDiv(rangeStart, stepNs) * stepNs`, then `+= stepNs` to
  `rangeEnd`), each with `{ns, label, leftPct}`. Label format is `HH:MM`
  for a ≥60s step, `HH:MM:SS` otherwise.
- `web/src/components/TimelineBar.tsx`: measures its own container's
  width once per `[timelines, rangeStart, rangeEnd]` change (`useRef` +
  `useEffect`, no `ResizeObserver`), feeds it to `computeTicks`, and
  renders a new sibling `.player-timeline-axis` row below `.player-
  timeline` — ticks as direct `position:absolute` children (no
  intermediate wrapper, same convention as segments/cursor), non-
  clickable (click-to-seek stays on the existing `.player-timeline`
  element only).
- `web/src/index.css`: `.player-timeline-axis`/`.player-timeline-tick`
  rules added alongside the existing `.player-timeline*` block.

## Tests

TDD red→green, all seams as planned:
- `web/src/pages/playerTimeline.test.ts`: `computeDataRange` spans
  min/max across channels with floor/ceil to whole seconds, and falls
  back to the given range when no segments exist.
- `web/src/pages/timelineTicks.test.ts`: a 1-hour range at ample width
  picks a round 10-minute step; the same range at a narrow width thins to
  30 minutes; a 10s range switches to `HH:MM:SS` labels; a degenerately
  narrow width still yields at least 2 ticks. Expected labels are derived
  via the already-tested `nsToDisplayString` (slicing out the `HH:MM`/
  `HH:MM:SS` portion) to stay independent of the test runner's timezone.
- `web/src/pages/PlayerPage.test.tsx`: new case — searching a wide window
  containing only a narrow real recording lands `playheadNs` on the
  recording's floored start, not the raw search `t1` (all 3 pre-existing
  cases, including the gap-skip wiring test, still pass unchanged).
- `web/src/components/TimelineBar.test.tsx`: new case — the axis row
  renders more than one tick at a stubbed 800px width, each tick a direct
  child of the axis row (no intermediate wrapper).

`npx vitest run` (98 tests, whole `web/` suite), `npx tsc --noEmit`, and
`npm run build` all clean. Regression-verified against the real Docker
stack too: `docker compose -f e2e/docker-compose.e2e.yaml up -d --build`
+ all 4 existing Playwright specs (`continuous-rotation.spec.ts`,
`journal.spec.ts`, `player-gap-skip.spec.ts`, `two-channel-playback.spec.ts`)
green (1.4m) on a freshly recreated stack — no changes needed to any of
them, confirming the new axis row/range-clipping didn't disturb the
`player-timeline-row`/`player-timeline-segment`/`player-current-time`
testids they depend on.

## Explicitly out of scope

- Resizing/`ResizeObserver` support.
- Multi-day range label handling (date rollover).
- Clickable tick labels.
- Any new e2e scenario for this ticket specifically (regression only).
