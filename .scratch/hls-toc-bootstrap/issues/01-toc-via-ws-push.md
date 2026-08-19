# Deliver TOC inline over WS instead of a follow-up HTTP GetTOC

Status: resolved

## Problem

`docs/docs/archive/12-hls-server.md` (§3, §4.1) and CLAUDE.md's
description of `EventPushServer` both describe `hls_server` as receiving
the TOC *inline* with `fblock.write.completed` over its WS subscription
("получает TOC целиком вместе с fblock.write.completed"). That's the
`include_toc` mechanism actually implemented in
`internal/api/eventpush.go` (`subscribeMessage.IncludeTOC` →
`tocPushMessage`, sent right after `EventFblockReady`).

The real code does not use it for `hls_server`. `internal/hlsclient`'s
`wireSubscribeMessage` (events.go) has no `IncludeTOC` field at all — only
`msm_server` (`internal/msmclient`) sets it. `internal/tocindex/subscriber.go`'s
`EventSubscriber.handleWriteCompleted` gets only a bare UUID over WS and
makes a separate synchronous HTTP round trip
(`hlsclient.Client.GetTOC` → `GET /storages/{id}/fcontainers/{uuid}/toc`)
before it can index the container — the code's own comment names this
explicitly: *"TOC is not inline with the push event"*.

So this is a doc/code mismatch, not just a missing optimization — the doc
already specifies the intended design.

## Why this matters

- Extra HTTP round trip (dial-free, but still a full request/response +
  TOC decode) per completed fblock, on the steady-state hot path, for
  every configured channel's `EventSubscriber`.
- `msm_server` already proves the `include_toc` path works end to end; the
  only gap is `internal/hlsclient.Client.Subscribe` never sets it and
  `tocindex.EventSubscriber` never consumes a `tocPushMessage`-shaped
  frame.

## Scope correction (2026-08-13)

`IncludeTOC` is currently wired **only** for the global (`Storage: ""`)
subscription path (`EventPushServer.serveGlobal`,
`internal/api/eventpush.go`) — the one `msm_server`/`internal/msmclient`
uses. `hls_server`'s `EventSubscriber` subscribes **per storage**
(`sub.Storage = s.storageID`, `ServeHTTP`'s main loop, same file, lines
~172–226) to get channel-filtered fblock events — a separate code path
that never checks `sub.IncludeTOC` at all today. So this issue isn't just
an `hlsclient`/`tocindex` change; it needs a matching addition on the
per-storage side of `EventPushServer` first.

## Recommendation

Confirmed with the user (2026-08-13): switch `hls_server`'s WS
subscription to request `include_toc`, and have `EventSubscriber` consume
the pushed TOC directly instead of issuing `GetTOC`. Concretely:

- **`internal/api/eventpush.go`**: in `ServeHTTP`'s per-storage loop, when
  `sub.IncludeTOC` is set and `ev.Name == storage.EventFblockWriteCompleted`,
  send a `tocPushMessage` right after the `pushMessage`, same as
  `serveGlobal` already does via `buildTOCPushMessage`. `unit` and
  `ev.UUID ([16]byte)` are already in scope here (no hex decode needed,
  unlike the global path's `JournalEvent.UUID string`) — factor
  `buildTOCPushMessage`'s body into a `unit`+`[16]byte`-taking helper both
  call sites share.
- Add `IncludeTOC` to `hlsclient.wireSubscribeMessage` / `Client.Subscribe`.
- Extend `hlsclient.Event` (or add a sibling decoded type) to carry the raw
  TOC bytes from a `tocPushMessage` frame, mirroring
  `internal/msmclient`'s existing handling of the same wire message.
- `tocindex.EventSubscriber.handleWriteCompleted` uses the pushed TOC
  directly (`toc.Decode`) when present, falling back to `GetTOC` when it
  isn't — `buildTOCPushMessage`'s own doc comment notes the source fblock
  can already be recycled past retention by push time, in which case the
  server sends no "toc" frame at all, not an empty one; the client can't
  assume presence just because it asked for `include_toc`.

This does not remove `GetTOC` from `hlsclient` — bootstrap (first connect,
post-disconnect gap-fill) and the steady-state fallback above still need a
way to fetch a TOC by UUID on demand for whatever isn't already
cached/pushed.

## Implementation (2026-08-13)

Done, TDD (`/tdd`), red→green per seam, in this order:

1. **`internal/api/eventpush.go`**: `ServeHTTP`'s per-storage loop now
   sends a `tocPushMessage` when `sub.IncludeTOC` and
   `ev.Name == storage.EventFblockWriteCompleted`, via a new
   `buildTOCPushMessageForUnit(unit, storageID, index, uuid [16]byte)`
   shared with `serveGlobal`'s `buildTOCPushMessage`. Tests:
   `TestEventPushServer_PerStorageIncludeTOC`,
   `TestEventPushServer_PerStorageWithoutIncludeTOC`
   (`internal/api/eventpush_test.go`).
2. **`internal/hlsclient`**: `wireSubscribeMessage`/`wirePushMessage` gained
   `IncludeTOC`/`TOC` fields; `Client.Subscribe` gained an `includeTOC bool`
   parameter (both existing call sites — `internal/hlsd.go`'s global
   channel-lifecycle subscription, `internal/tocindex/subscriber.go` —
   updated, the former passing `false`); `Event` gained a `TOC []byte`
   field. Test: `TestClient_Subscribe_IncludeTOC`
   (`internal/hlsclient/hlsclient_test.go`).
3. **`internal/tocindex/subscriber.go`**: `EventSubscriber` now subscribes
   with `includeTOC=true`. `follow`'s dispatch loop goes through a new
   `eventReader` (one-slot pushback buffer): `handleWriteCompleted` looks
   for the paired "toc" frame via `eventReader.tryNextTOC` (bounded by
   `tocPushTimeout = 2s`, matching `internal/msmd`'s analogous
   `tocWaitTimeout` pattern but pushing back a non-matching message instead
   of dropping it — hls_server needs a complete index, unlike msm_server's
   best-effort delivery) and decodes it directly; `GetTOC` is only called
   as a fallback (no toc frame arrived in time, or it failed to decode).
   Test: `TestEventSubscriber_LiveEventSkipsGetTOC`
   (`internal/tocindex/subscriber_test.go`) — asserts zero `GET .../toc`
   calls for a live event, via a counting middleware wrapped around the
   test server's handler, baselined after bootstrap (which still legitimately
   uses `GetTOC`).

Slice 5 (fallback-without-a-toc-frame, needing a hand-rolled fake WS
handler rather than the real server) was explicitly deferred by agreement
with the user — not implemented, low risk, can be its own follow-up ticket
if it turns out to matter in practice.

`go build ./...`, `go vet ./...`, full `go test ./...`, and
`golangci-lint run ./...` all clean after this change.
