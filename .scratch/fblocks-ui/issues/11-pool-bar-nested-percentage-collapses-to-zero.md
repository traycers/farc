# Pool status bar's fill sections render at 0px in every browser — nested percentage width inside an auto-sized flex wrapper

Status: fixed (2026-08-18)

## Symptom

User ran the real stack (`docker compose up -d`, real cameras/channels, not
e2e). On the Fblocks status page, the fblock-state squares updated live and
correctly as recording progressed, but the pool-status-list bars (above the
squares) stayed visually empty the whole time — no colored fill ever
appeared, across a fresh page load, a hard reload with cache cleared, and
an incognito window, in the user's actual browser (Chrome/Chromium,
`Приватный просмотр`).

## Investigation (ruled out first)

Before suspecting the frontend at all, the backend/transport were checked
directly against the live stack (`farc-farc-1`/`farc-web-1`, real cameras,
not e2e), each time confirming `content_size` growing correctly:

- A raw Node `WebSocket` client connected directly to `farc:8081/ws`
  (bypassing nginx entirely): received a live-growing `content_size` every
  ~500ms, plus `fblock.write.completed`/`.started` events on rotation.
- The same client through nginx's real proxy path
  (`ws://web:80/api/events/ws`, matching `subscribeStorageEvents`'s exact
  subscribe payload): identical, correct, growing data.
- The user's own DevTools → Network → WS → Messages tab (i.e. the literal
  bytes arriving at their real browser): confirmed "pool" frames arrive
  with `content_size` genuinely growing.

So the bug was never in `internal/api/eventpush.go`'s `poolPollInterval`
ticker, `internal/storage/pool.go`'s `Slots()`, or nginx's `/api/events/`
proxy config — all independently confirmed correct, live, on this exact
deployment.

**A methodological mistake along the way**: an initial headless-Chromium
check (Playwright) read each `.pool-section`'s `style` attribute string
(e.g. `"width: 69.27%"`) and saw it updating correctly, and was wrongly
taken as proof the UI itself rendered correctly. It doesn't — see below.
Only checking real geometry (`getBoundingClientRect()`) exposed the actual
bug, and did so in **both** Chromium and Firefox identically, meaning this
was never browser-specific either.

## Root cause

`PoolStatusList.tsx` (pre-fix) nested the five section `<span>`s inside two
wrapper `<div>`s, `.pool-section-left` (prolog/catalog/content) and
`.pool-section-right` (toc/epilog), themselves flex children of
`.pool-section-bar` with `justify-content: space-between` and no explicit
width on either wrapper. Each inner `.pool-section` set its own width as a
CSS percentage (`pct(bytes, fblockSize)`).

A percentage `width` only resolves against a **definite** containing-block
width. `.pool-section-left`/`-right` have no explicit width — their own
size is "auto" (shrink-to-fit based on their children's intrinsic size).
Per the CSS spec, this makes the wrapper's width indeterminate for the
purpose of resolving *its own children's* percentage widths, since those
children have no other intrinsic size to contribute (an empty `<span>`).
The result: every `.pool-section`'s real rendered width is `0px`,
regardless of what percentage the inline `style` attribute says — confirmed
directly via `getBoundingClientRect()` in a headless run against the exact
live stack:

```
chromium left group box: {"width":0, ...}
chromium content box:    {"width":0, ...}
firefox  left group box: {"width":0, ...}
firefox  content box:    {"width":0, ...}
```

The `.pool-slot-square` state indicator (blue/gray/etc.) is styled purely
via a CSS class, independent of any of this — which is exactly why it kept
updating correctly while the bar next to it stayed empty, matching the
reported symptom precisely.

This is a plain CSS layout bug, 100% reproducible, unrelated to timing,
browser, network, or the backend/WS feed — likely present since issue 04's
original implementation, just never caught because
`PoolStatusList.test.tsx` (jsdom, no real layout engine) only ever asserted
on the `style` attribute string, which was always correct.

## Relationship to issue 09

`.scratch/fblocks-ui/issues/09-pool-bar-static-on-fresh-deploy.md` looks
superficially similar (pool bar not moving) but is a **different bug**: that
investigation's own direct WS checks showed `content_size` itself not
changing at the backend/wire level at all (a genuine data stall, since
resolved as unreproducible). This ticket's bug is the opposite — data was
always correct and live, only the rendering was broken. Both can't have had
the same root cause; 09 stays open/inconclusive as its own thing.

## Fix

`web/src/components/PoolStatusList.tsx`: removed the `.pool-section-left`/
`.pool-section-right` wrapper `<div>`s entirely. Each of the 5
`.pool-section` spans is now a **direct** child of `.pool-section-bar`
(`position: relative`), absolutely positioned via inline `left`/`right`
(computed in JS, not CSS): prolog/catalog/content get `left: <prefix sum of
earlier widths>%`, toc/epilog get `right: <suffix sum of later widths>%` —
new `prefixOffsets`/`suffixOffsets` helpers. This entirely sidesteps the
indeterminate-containing-block problem: every percentage now resolves
against `.pool-section-bar`, which has a real, definite pixel width from
its own `flex: 1 1 auto` sizing in the row.

`web/src/index.css`: `.pool-section-bar` gained `position: relative`
(dropped `display: flex`/`justify-content: space-between`, no longer
needed); `.pool-section-left`/`-right` rules removed entirely;
`.pool-section` is now `position: absolute; top: 0; height: 100%`.

Verified fixed via the same `getBoundingClientRect()` check, against a
rebuilt `farc-web-1` image, live on the real stack:

```
chromium t=1s content: {"width":284.125, ...}   (was 0)
chromium t=5s content: {"width":456.98,  ...}   (growing)
firefox  t=1s content: {"width":513.78, ...}    (was 0)
firefox  t=5s content: {"width":687.42, ...}    (growing)
```

A screenshot at this point showed the expected visible blue (content) bar
filling most of the row, with a thin orange (toc) sliver at the right edge.

**Test**: `PoolStatusList.test.tsx`'s old "groups ... in a left-anchored
group and ... right-anchored group" test (which only checked wrapper
`.children` class order — exactly the kind of assertion that couldn't have
caught this) replaced with one asserting the new `left`/`right` offset
values directly (still a `style` attribute assertion, since jsdom has no
real layout engine — the actual regression-proofing here is the
`getBoundingClientRect()` check done manually via Playwright against the
live stack, not something jsdom can express). No new permanent e2e spec
added — this was verified via an ad hoc Playwright script against the real
docker-compose stack, not committed to the repo.
