# Live page: recording/connection status doesn't update after toggling

Status: fixed (2026-08-21, via /mattpocock-skills:tdd)

See `.scratch/live-page-fixes/spec.md` for the full design conversation
this was split from.

## Goal

`web/src/pages/LivePage.tsx:41-43` fetches `listChannels()` once on mount
and never refreshes afterward, so a channel's `recording`/
`last_connect_error` fields (shown via `ChannelStatusIndicator`) go stale
the moment recording is toggled off/on while the page stays open.

## Scope

- Copy `ChannelsIndexPage.tsx:46-63`'s `subscribeJournal` effect into
  `LivePage.tsx`, same shape: on `channel.recording.started`/`stopped`,
  update the matching channel's `recording` field in state; on
  `channel.rtsp.connect_failed`/`connected`, update `last_connect_error`.
  Same scope boundary as that page — no live add/remove-channel handling,
  only status-field updates for channels already in `channels` state (the
  initial `listChannels()` on mount is still this page's only resync
  point for the channel *list* itself, matching every other page's
  convention per `.scratch/web-ui-fixes/spec.md`).
- `LivePage.tsx` does not currently show a `connectFailedBanner` the way
  `ChannelsIndexPage` does — this issue does not add one; only the
  `ChannelStatusIndicator`'s outer ring (driven by `last_connect_error`)
  and inner dot (driven by `recording`) need to reflect current state.

## Explicitly out of scope

- `ChannelStatusIndicator.tsx` itself — it's a pure props-driven component,
  no bug there; it already renders whatever it's given correctly.
- Any new banner/toast on `LivePage` for connect failures.

## Comments
