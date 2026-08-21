# mediamtx as a real production component (not just dev/e2e)

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/live-page/spec.md` for the full design conversation this
was split from.

## Goal

Today mediamtx only exists as a throwaway RTSP test-source generator in
`docker-compose.yaml` and `e2e/docker-compose.e2e.yaml` (it accepts an
RTSP publish from a looping ffmpeg container and serves it back — see the
comment in `e2e/docker-compose.e2e.yaml`). This ticket makes it a real,
persistent production component: the sole thing that connects to a
camera's RTSP stream, re-serving that stream both as RTSP (for farcd's
ingest) and WebRTC/WHEP (for the web app's Live page).

## Scope

- Add mediamtx to the production docker-compose stack as a long-running
  service (distinct from — or replacing the role of — the existing dev/
  e2e-only mediamtx entries, which were a test *source*, not a real proxy
  target).
- mediamtx configuration must have **WebRTC enabled** (explicit
  requirement) so it can serve WHEP for live viewing.
- mediamtx must be reachable by:
  - `apid`, over its REST config API (default port `9997`), to add/
    update/remove paths as channels are created/edited/removed
    (`issues/01-apid-server.md`).
  - farcd's ingest (`internal/ingest`, `gortsplib`), over RTSP (default
    port `8554`), to pull the re-served stream instead of connecting to
    the camera directly.
  - the browser, over WHEP (mediamtx's default WebRTC HTTP port, `8889`),
    to establish the live-view WebRTC session directly — WHEP's signaling
    is HTTP but the resulting media path needs real ICE connectivity to
    mediamtx, which has network/NAT implications for whoever owns the
    deployment topology (document this as a known operational
    consideration, not something this ticket needs to solve generically).
- Update whichever nginx config (`web/nginx.conf`) needs a proxy path if
  the browser should not need to know mediamtx's address/port directly.

## Known constraint (documented, not solved here)

Many IP cameras cap the number of concurrent RTSP sessions they'll serve.
This design deliberately keeps that count at exactly one per camera —
mediamtx's — with farcd consuming the stream *through* mediamtx rather
than opening its own second RTSP session to the camera. Don't add a
farcd-direct-to-camera fallback path that would reintroduce a second
session.

## Comments
