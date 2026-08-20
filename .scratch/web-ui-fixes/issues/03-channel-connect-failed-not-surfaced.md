# 03 — Channel connect failures never surface (Журнал + Channels page)

Status: resolved

## Task

Root cause: `ChannelIngest.setConnected` (`internal/ingest/channelingest.go:127-137`)
only invokes `onConnectionChange` when `connected` actually changes value. A
freshly-added channel starts with `connected=false`, so its first failed
RTSP connection attempt is a `false→false` no-op — nothing reaches any hook,
event, or WS message; the only trace today is a `levellog.Warn` inside
`runReconnecting` (`channelingest.go:170-195`, line 183) that never leaves
the server process.

Backend:

1. Add a new event, distinct from `EventChannelRTSPDisconnected`
   (`internal/api/eventpush.go:170-183`, which documents itself as meaning
   "lost/regained a previously-working connection" — a different condition
   from "never connected"). Suggested constant:
   `EventChannelRTSPConnectFailed = "channel.rtsp.connect_failed"`.
2. In `runReconnecting` (`channelingest.go:170-195`), fire this new event on
   every failed attempt while the channel has never yet successfully
   connected (not gated on a state flip, and with no retry-count/time
   threshold — fire on the very first failure). Once the channel connects at
   least once, subsequent behavior reverts to the existing
   `EventChannelRTSPDisconnected`/`EventChannelRTSPConnected` flip-based
   events, unchanged.
3. Populate `JournalEvent.Reason` (`eventpush.go:44-55`/`193-203`, currently
   unused for channel events) with the real underlying error text from
   `runErr` in `runReconnecting` — same string that's currently only logged.
4. Set `JournalEvent.Severity` to `error` for this new event.
5. Add a `last_connect_error` string field (empty when none) to
   `channelInfo` (`internal/api/channels.go:207-224`) / `handleListChannels`,
   so `GET /channels` reflects the current failure state even for a client
   that wasn't listening on the WS feed at the moment it happened. Clear it
   once the channel successfully connects.

Frontend:

6. Subscribe `ChannelsIndexPage.tsx` to the global WS event feed (it
   currently has no subscription at all — pattern after `subscribeJournal`
   in `web/src/api/events.ts`, already used by `JournalPage.tsx`).
7. On receiving `channel.rtsp.connect_failed` (live) or seeing a non-empty
   `last_connect_error` (on load), show an inline `alert alert-danger`
   banner on the Channels page (reuse the existing
   `{error && <div className="alert alert-danger">{error}</div>}` pattern
   already used throughout `web/src/pages/**`) and a persistent per-channel
   indicator in the channels table naming the error. Don't build a
   toast/notification component — no such primitive exists anywhere in this
   codebase and this is a deliberate decision to not introduce one here.
8. Confirm `ChannelInfo` (`web/src/api/farcd.ts:148-156`) picks up the new
   `last_connect_error` field.

## Tests

Backend: wherever `setConnected`/`runReconnecting` is currently tested in
`internal/ingest` — cover a channel that has never connected getting the new
event/reason on its first failed attempt, and that a previously-connected
channel dropping still gets the existing disconnect event, not the new one.
`internal/api` tests for the new `last_connect_error` field round-tripping
through `GET /channels`.

Frontend: Vitest + `@testing-library/react` for the WS subscription and
banner/indicator rendering on `ChannelsIndexPage`.

## Verify

`go test ./internal/ingest/... ./internal/api/...`, `cd web && npx tsc -b &&
npx vitest run`. Manually verify against a running farcd: add a channel with
a garbage `rtsp://` URL, confirm the banner appears promptly on the Channels
page and the per-channel error persists across a page reload; confirm a
previously-working channel whose stream drops still shows the pre-existing
disconnect behavior unchanged.

## Comments

2026-08-20: Implemented test-first. Backend: `ChannelIngest` gained
`everConnected`/`lastConnectError`/`connectFailedReported` plus
`SetOnConnectFailed`/`LastConnectError()` (`channelingest.go`), firing once
per never-connected streak from `runReconnecting` — covered by two new
`reconnect_test.go` cases (fires-once-before-first-success,
not-fired-once-ever-connected). New event constant
`EventChannelRTSPConnectFailed = "channel.rtsp.connect_failed"`
(`eventpush.go`), `LastConnectError` threaded through
`ingest.ChannelInfo`/`IngestManager.List()` and
`api.channelInfo`/`handleListChannels` (`last_connect_error` JSON field),
each covered by a real-unreachable-URL test in `ingestmanager_test.go`/
`channels_test.go`. `farcd.go` wires `SetOnConnectFailed` to
`push.Publish(..., Severity: "error", Reason: err.Error())`, same
(untested-at-that-layer) convention as the pre-existing
`SetOnConnectionChange` wiring.

Found and fixed a regression this surfaced:
`TestRun_UpdateChannelOverHTTP_StorageUnchanged_PublishesNoGlobalEvent`
assumed zero channel events of any kind after a same-storage update: since
`handleUpdateChannel` always restarts the channel's `ChannelIngest`
under the hood, a legitimate `channel.rtsp.connect_failed` now fires for the
freshly-restarted (still-unreachable) ingest. Narrowed that test's assertion
to its actual concern — no `channel.removed`/`channel.created` churn — since
that's what the test's own doc comment says it's guarding, not "zero events
ever."

Frontend: banner + persistent per-row error text — see `issues/04`'s
comment, implemented together with its WS subscription.
