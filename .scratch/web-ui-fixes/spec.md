# Web UI fixes — player search, journal connect errors, channel recording status

Status: open — grilled 2026-08-20

Three unrelated small UI/backend bugs reported together in one batch and
filed under one directory for tracking convenience only — there's no shared
code path between them beyond touching `web/src/pages/**` and
`internal/api`/`internal/ingest`.

## Decisions (settled during grilling)

1. Filed as one `.scratch/web-ui-fixes/` directory with 4 separate issue
   files (Player has two independent fixes, not one).
2. Player search button (`PlayerPage.tsx:121-123`): disable it entirely when
   no channel is checked, rather than allowing the click and showing a
   validation error. No `disabled` attribute exists there today.
3. "Jump to first available fragment" is already effectively implemented
   via `computeDataRange` (`playerTimeline.ts:51-62`), which sets
   `playheadNs` to the earliest segment `begin` across all *selected*
   channels. The only real gap: when the search range has zero segments
   across all channels, `computeDataRange` silently falls back to the
   requested `[t1, t2]` with no indication to the user. Add an explicit
   "no records in range" message, following this repo's only existing
   pattern (`{error && <div className="alert alert-danger">{error}</div>}`).
4. Channel connect-failure (Журнал page): root cause found —
   `ChannelIngest.setConnected` (`internal/ingest/channelingest.go:127-137`)
   only fires `onConnectionChange` on an actual state *flip*. A brand-new
   channel starts `connected=false`, so its first failed connection attempt
   is `false→false` and never fires anything — not even the server-side log
   line reaches any event. Decisions:
   - New event, not a reuse of `EventChannelRTSPDisconnected`
     (`internal/api/eventpush.go:170-183`) — that event's doc comment
     explicitly means "lost/regained a previously-working connection," a
     different condition from "never connected." New constant, e.g.
     `EventChannelRTSPConnectFailed = "channel.rtsp.connect_failed"`.
   - Fire on the very first failed attempt, no debounce/threshold — false
     positives on a flaky-but-transient reconnect are an acceptable cost
     against the alternative (a genuinely broken URL showing nothing for N
     seconds/retries).
   - Severity `error` (reusing `JournalEvent.Severity`, currently populated
     only for `fblock.*` events).
   - Populate `JournalEvent.Reason` with the actual underlying error text
     from `runErr` in `runReconnecting` (`channelingest.go:170-195`) —
     currently only reaches `levellog.Warn`, never any event.
   - Surfaced on the Channels page as an inline `alert-danger` banner (no
     toast component exists anywhere in `web/src` — this repo's only
     pattern is inline alerts; not introducing a new UI primitive for
     this), since `ChannelNewPage` already navigates to `/channels` right
     after the (synchronous) `POST /channels` succeeds — by the time the
     async RTSP connect fails, the user is already on that page.
   - Also needs a *persistent* per-channel indicator on the Channels list
     (not just a one-shot banner) — a `GET /channels` field, e.g.
     `last_connect_error` (string, empty when none), so this is visible on
     page reload too, not only to whoever had the page open at the exact
     moment.
5. Channel capture-status indicator (Channels page): a 2-color dot, red =
   `Recording=true` right now, gray = not recording — deliberately *not* a
   3rd color for connect errors (that's decision 4's separate
   banner/indicator; don't conflate the two axes — `Connected` and
   `Recording` are independent per `internal/ingest/policy.go`/
   `channelingest.go`).
   - `Recording` already exists server-side (`CapturePolicy.recording`,
     `LiveSnapshot()`, `internal/ingest/policy.go:342-350`) and is already
     pushed over the WS feed (`EventRecordingStarted`/`EventRecordingStopped`,
     `internal/api/eventpush.go:176-177`) — but never through REST. Add
     `recording: bool` to `GET /channels`' `channelInfo`
     (`internal/api/channels.go:207-224`).
6. Both 4 and 5 require `ChannelsIndexPage.tsx` to subscribe to the global
   WS event feed (it currently doesn't — only the Журнал/fblock-tree/
   fblocks-grid pages do, via `web/src/api/events.ts`) so the dot and the
   connect-error banner/indicator update live, not just on page
   load/refresh.

## Issues

- `issues/01` — Player search button stays enabled with no channel selected
- `issues/02` — Player search shows nothing when zero fragments match
- `issues/03` — Channel connect failures never surface (Журнал + Channels page)
- `issues/04` — Channel recording-status indicator (red/gray dot)
