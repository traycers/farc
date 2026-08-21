# Web: shared two-circle channel status indicator

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

A single shared status component used by both `ChannelsIndexPage` and
the new Live page (`issues/04-web-live-page.md`), instead of duplicating
status markup/CSS across the two.

## Scope

- New component, e.g. `web/src/components/ChannelStatusIndicator.tsx`,
  rendering two concentric circles:
  - **Outer ring** — farcd's own RTSP connection status: blue when
    connected, red when `last_connect_error` is set on the `ChannelInfo`.
    This is unchanged from what `last_connect_error` already means today
    — after the mediamtx topology change (`issues/02-...`), farcd
    connects to mediamtx rather than the camera directly, so this same
    field is still an accurate live-or-not signal (see spec.md's "Single
    RTSP connection to the camera").
  - **Inner dot** — recording vs idle, identical semantics to today's
    `.status-dot`/`.status-dot-recording`/`.status-dot-idle` classes on
    `ChannelsIndexPage.tsx`.
- `ChannelsIndexPage.tsx`'s status `<td>` switches from its current
  inline `<span className="status-dot ...">` to this shared component.
  Keep the existing `last_connect_error` text line and the page-level
  `connectFailedBanner` — only the dot markup itself is replaced.
- Reuse the existing CSS naming convention/documentation style already
  present in `web/index.css` (the current `.status-dot` comment block
  documents intentional 2-state design; update/extend it rather than
  leaving stale comments describing the old single-dot behavior).

## Comments
