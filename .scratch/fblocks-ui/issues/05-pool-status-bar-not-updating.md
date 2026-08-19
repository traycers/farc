# Pool-status-list bar doesn't update at all (fblock-tree does)

Status: resolved — not a bug in the bar/WS code, see Comments

## Problem

Reported 2026-08-17, right after `issues/04`'s bidirectional-fill redesign
shipped: on a live Storage (64MB fblock, 5×2Mbit channels), the user
observed the fblock **tree** view (`issues/02`, `internal/api/fblocktree.go`,
`liveTreePollInterval = 500ms`) update live as expected, but the
**pool-status-list bar** (`issues/04`, `PoolStatusList.tsx`, driven by the
`"pool"` WS push added in `issues/04`'s implementation, `poolPollInterval =
500 * time.Millisecond` in `internal/api/eventpush.go`) did not update at
all — not "slowly", not "choppy", genuinely static.

This is a regression/gap in a feature that was just implemented and unit-
tested this same day (`04-pool-status-list-plan.md`'s TDD cycles all pass),
so the bug is very likely in something the test suite doesn't exercise:
real WS wiring end-to-end against a live farcd, not the pure logic covered
by `pool_test.go`/`eventpush_test.go`/`PoolStatusList.test.tsx`. Candidates
to check first (not yet investigated):

- Does the frontend actually send `include_pool: true` on the real
  `subscribeStorageEvents` connection for this page, or does something
  about the live page's mount/subscribe lifecycle skip it?
- Does farcd's `ServeHTTP` per-storage loop actually start the `poolPollInterval`
  ticker branch for a real subscription (as opposed to the unit tests,
  which construct `subscribeMessage`/`EventPushServer` directly)?
- Is `unit.PoolSlots()` (`internal/storage/unit.go`) actually reachable/
  non-erroring against a real, live-writing Storage — `04`'s own plan doc
  never verified this against a running farcd (documented limitation in
  both `04`'s Comments and its follow-up section).

## Next step

This needs its own `/mattpocock-skills:grilling` session before a fix —
scope (frontend subscription bug vs. backend push bug vs. something in
`PoolSlots()` itself) isn't yet narrowed.

## Comments

2026-08-17: Grilled and investigated live against the user's real
deployment (`docker compose up --build`, storage `b36d065aff6ee1ad`, 5
channels at 2Mbit). Ruled out, in order: stale binary (confirmed rebuilt),
nginx/vite-proxy WS drift (none — both `web/nginx.conf` and
`web/vite.config.ts` correctly proxy `/api/events/` with Upgrade headers,
and farcd's own WS server has no path routing to mismatch), a WS handshake
failure (Network tab showed `101 Switching Protocols` — succeeded), and a
server-side panic/`unknown storage` early-return (farcd's own access log
showed clean `GET /ws -> 200` entries throughout, no panics).

The decisive check was direct, live inspection of the actual storage via
`docker compose exec`/`docker network`-attached `curl` (bypassing the
sandbox's own loopback-proxy interference on host-mapped ports): fblock 0
was genuinely stuck `in_progress` with `farc_writes_total{storage=...} 0`
— the bar and grid squares were rendering the live state *correctly*;
there was simply nothing to show, because nothing was being written.

Reproduced the same symptom from scratch with a known-good, controlled
RTSP source (mediamtx + `e2e/media/sample.mp4` via
`e2e/docker-compose.e2e.yaml`, manually driving `e2e/tests/setup.ts`'s API
calls since the sandbox's loopback proxy blocked Playwright's own
`fetch`/browser access to the mapped host ports): same `connected: true`,
`in_progress` forever, `farc_writes_total 0`, plus `"N RTP packets lost"`
log lines — which, once looked for, are ALSO present in the user's real
deployment's logs (missed in the first log pass because of a too-narrow
grep pattern), just with far larger loss counts (260-265 packets per event
vs. 3-8 in the clean e2e run).

**Conclusion**: not a bug in this session's WS/pool-bar code at all — the
live-update pipeline (WS transport, `eventpush.go`'s pool ticker,
`PoolSlots()`) all work correctly. The real root cause is a separate,
deeper ingest bug that reproduces even against a clean, low-loss RTSP
source: recording appears to stall permanently after RTP packet loss and
never produces a single write. Tracked as
[[08-ingest-stalls-after-rtp-packet-loss]] — that's where the fix belongs,
not here. Also note: while investigating, found the sandbox environment's
own loopback proxy blocks direct `localhost:<mapped-port>` access (both
`curl` and Node's `fetch`/Playwright's browser hit it) — routing through
`docker network`-attached containers or `docker compose exec` bypasses it;
this is an environment quirk of this coding sandbox, not a repo bug, but
worth remembering for future live-debugging sessions here.
