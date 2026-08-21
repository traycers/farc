# Docs: update scope docs + new ADR for apid/mediamtx live-viewing

Status: open

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

Keep the design docs authoritative (per this repo's own convention that
`docs/docs/archive/` records *why*, not just *what*) after this feature
reverses an explicit prior scope decision.

## Scope

- `docs/docs/archive/12-hls-server.md` currently states (line 8) that
  live viewing is out of farc's scope, handled externally by a
  third-party RTSP proxy such as mediamtx, before archival. That
  statement becomes false once this feature ships — the web SPA and a
  new in-repo binary (`apid`) now do live viewing, with mediamtx
  configured/orchestrated by `apid`. Update this doc.
- New ADR, next number after `021`
  (`docs/docs/archive/adr/adr-022-...` or whatever the actual next-free
  number is at implementation time), recording:
  - `apid` as the channel-write orchestration point across farcd and
    mediamtx (why it's a separate binary, not a farcd extension).
  - The single-RTSP-connection-to-camera topology (mediamtx pulls the
    camera; farcd pulls mediamtx) and why (camera concurrent-session
    limits).
  - No-rollback/idempotent-retry semantics for `apid`'s cross-service
    writes.
- `PLAN.md` — add a phase entry once implemented (this file tracks the
  web/deployment build-out phase by phase; CLAUDE.md requires updating it
  when a phase lands).
- `CLAUDE.md` — update "Code layout" (`cmd/apid/`, new `internal/`
  packages) and "Architecture" once the above lands, since CLAUDE.md
  itself says it and PLAN.md drift out of date otherwise.

## Comments
