# LiveVideoTile shows the raw thrown error instead of "нет сигнала"

Status: fixed (2026-08-21, via TDD)

Found while diagnosing a real WHEP `400 Bad Request` for a channel with no
mediamtx path (see `.scratch/live-page/spec.md`'s "channels created before
this change have no mediamtx path" migration note) — the tile displayed
`Error: whep: POST /api/whep/3/whep: 400 Bad Request` verbatim instead of
the "нет сигнала" placeholder the original design called for
(`.scratch/live-page/issues/04-web-live-page.md`: "failed-connection state
renders the same placeholder pattern... нет сигнала").

## Fix

`LiveVideoTile.tsx`: any `connectWhep` rejection (mediamtx has no source,
a transient network error, a real HTTP error, ...) now shows the same
"нет сигнала" placeholder as the no-URL case, since none of those causes
are actionable or meaningful to the viewer as raw text. The real error is
still `console.error`'d for debugging. Kept as a separate, distinct
message only the case a real user-facing distinction exists: this
browser has no `RTCPeerConnection` at all ("Этот браузер не поддерживает
WebRTC.").

## Comments

Covered by a new `LiveVideoTile.test.tsx` case: stubs `RTCPeerConnection`
(so the code reaches the real connect path) and mocks `connectWhep` to
reject with a realistic error, asserting the placeholder shows "нет
сигнала" and not the error text. `npm test` 173/173, `npm run build`
clean, rebuilt and redeployed to the reporting user's live stack.
