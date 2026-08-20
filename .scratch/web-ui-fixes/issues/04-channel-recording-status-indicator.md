# 04 — Channel recording-status indicator (red/gray dot)

Status: open

## Task

`CapturePolicy.recording` already exists (`internal/ingest/policy.go:69`,
exposed read-only via `LiveSnapshot()` at lines 342-350) and already drives
WS events (`EventRecordingStarted`/`EventRecordingStopped`,
`internal/api/eventpush.go:176-177`) — but is not exposed over REST, and the
Channels page has no status column or WS subscription at all today.

Backend:

1. Add `recording: bool` to `channelInfo` (`internal/api/channels.go:207-224`)
   / `handleListChannels`, sourced from the same `CapturePolicy.LiveSnapshot()`
   used by the WS path.

Frontend:

2. Add a status column to `ChannelsIndexPage.tsx`'s table (alongside the
   existing `id`/`name`/`policy`/`prerecord`/`postrecord`/`actions` columns,
   `ChannelsIndexPage.tsx:109-116`) rendering a small circular dot: red when
   `recording=true`, gray when `recording=false`. This is a 2-state
   indicator only — do not fold `Connected` (RTSP session state) into it; a
   disconnected channel is definitionally not recording, so it already
   renders gray, and connect-failure gets its own indicator (see
   `issues/03`) rather than a third dot color.
3. Subscribe `ChannelsIndexPage.tsx` to the global WS event feed (same
   subscription work as `issues/03` step 6 — if both issues are implemented
   together, share the one subscription) so the dot flips live on
   `channel.recording.started`/`channel.recording.stopped` without a page
   reload.
4. Update `ChannelInfo` (`web/src/api/farcd.ts:148-156`) to include
   `recording`.

## Tests

Backend: extend whatever currently tests `handleListChannels` in
`internal/api` to assert `recording` round-trips correctly for both states.
Frontend: Vitest + `@testing-library/react` — dot renders red/gray per
`recording`, and flips on a simulated WS event.

## Verify

`go test ./internal/api/...`, `cd web && npx tsc -b && npx vitest run`.
Manually verify against a running farcd: start/stop recording (continuous or
triggered) on a real channel, confirm the dot flips live on the Channels
page without a reload.

## Comments
