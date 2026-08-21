# Live page fixes — row layout, stale recording status, WHEP CORS/403

Status: implemented (2026-08-21, via `/mattpocock-skills:tdd`) — see `issues/01`–`05`, all fixed. `issues/04`–`05` were found while verifying `01`–`03` against the reporting user's live deployment, not part of the original grilled batch.

## Problem

Three unrelated small bugs on the Live page (`web/src/pages/LivePage.tsx`,
`.scratch/live-page/`), reported together in one batch. Filed as one
directory for tracking convenience only, following the `.scratch/web-ui-fixes/`
precedent (multiple unrelated small bugs reported together get one
`spec.md` + one issue file per bug, not one directory per bug) — there is
no shared code path between them beyond touching `LivePage.tsx` and its
supporting components.

## Decisions (settled during grilling, 2026-08-21)

1. **Row layout** (`issues/01`): the per-channel row currently orders
   checkbox → status indicator → name → "в архив" text button
   (`LivePage.tsx:81-96`), with the button pushed to the row's far right via
   Bootstrap's `ms-auto`. Variable-length channel names before a
   right-anchored button don't actually misalign visually today (the button
   is always right-anchored regardless of name length) — but the requested
   fix stands on its own terms: turn the archive link into an icon-only
   button and move it to the front of the row, so the name — now the only
   variable-width element — ends up last, giving every row a stable
   fixed-width prefix. Minimal reorder: icon → checkbox → indicator → name
   (checkbox/indicator order unchanged, button just moves from end to
   start). Icon: add the `bootstrap-icons` npm package (already pairs with
   the existing Bootstrap 5 dependency, `bi bi-clock-history` — "history/
   last hour" reads more accurately than a generic film icon for "jump to
   this channel's archive over the last hour"), with `title`/`aria-label`
   `"Открыть в архиве"` since the button loses its text label.

2. **Recording status doesn't update after toggling** (`issues/02`): root
   cause confirmed by reading the code — `LivePage.tsx:41-43` fetches
   `listChannels()` once on mount and never refreshes `recording`/
   `last_connect_error` afterward. `ChannelsIndexPage.tsx:46-63` already
   solves exactly this via `subscribeJournal` (`web/src/api/events`),
   handling `channel.recording.started`/`stopped` and
   `channel.rtsp.connect_failed`/`connected` events to keep those two
   fields current for already-known channels (no live add/remove-channel
   handling — that boundary is deliberate there, per
   `.scratch/web-ui-fixes/issues/03,04`, and this fix copies the same
   boundary, not more). `LivePage.tsx` gets the identical subscription,
   copied verbatim in shape.

3. **WHEP fails with `NetworkError`/CORS + 403 in the console**
   (`issues/03`): diagnosed against a real mediamtx v1.20.0 instance (this
   repo's pinned version) — 6 real request permutations (active source,
   missing path, no active source i.e. the exact apid→mediamtx→camera
   topology, widened and stock default `authInternalUsers`, with/without
   an `Origin` header) never produced a 403; every response, success or
   error, carried `Access-Control-Allow-Origin`. mediamtx's own CORS/auth
   handling is not the cause. User confirmed a local Squid proxy is running
   on the machine reproducing the bug — a live repro of hitting a
   host-published mediamtx port through a similarly-configured local proxy
   produced an *identical*-shaped failure (its own `403 Forbidden` HTML
   page, no ACAO header) purely from the proxy's own interception, before
   the request ever reached mediamtx. Root cause: `web/src/api/whep.ts`
   makes a direct cross-origin browser request to
   `http://localhost:8889/{id}/whep` (mediamtx's own port), bypassing
   nginx entirely — unlike every other backend (farcd, apid, hlsd), which
   the web app already reaches through `/api/*` proxied through nginx on
   its own origin. A local proxy configured to intercept traffic to that
   port (Squid here; equally plausible: another user's antivirus web
   filter, corporate proxy, or browser extension) sits in front of
   mediamtx and produces exactly this signature. Fix: route WHEP signaling
   through nginx too, same-origin, same pattern as the other three
   backends — this doesn't just fix this one machine, it removes the
   entire class of "something intercepts the direct mediamtx-port request"
   failures for every deployment. Media itself (RTP/ICE over UDP) is
   unaffected — only the HTTP POST/DELETE signaling goes through nginx.
   - New `nginx.conf`/`vite.config.ts` proxy block: `/api/whep/` →
     `mediamtx:8889/` (same `location .../ { proxy_pass http://X:PORT/; }`
     prefix-strip pattern already used for `/api/apid/`, `/api/hls/`).
   - **Non-obvious gotcha, confirmed against mediamtx's actual source**
     (`sessionLocation` in `internal/servers/webrtc/http_server.go`,
     v1.20.0 — the version this repo pins): the `Location` header mediamtx
     returns on a successful WHEP POST is a **path-absolute, host-relative**
     string (`/{path}/whep/{secret}`), not a full URL. `whep.ts` resolves
     it via `new URL(location, url)`, and per standard URL resolution, a
     leading-`/` reference replaces the *entire path* of the base URL, not
     just the part after the proxy prefix — so without an explicit fix,
     the teardown `DELETE` would silently escape the `/api/whep/` prefix
     entirely once proxied, landing on the bare origin root, which nginx's
     SPA fallback (`try_files ... /index.html`) would swallow as a
     `200 text/html` response the caller ignores (the abort handler's
     `fetch(...).catch(() => {})` hides any error) — WHEP sessions would
     leak on mediamtx with no visible symptom. Fix: nginx's `proxy_redirect`
     directive on the new `/api/whep/` block rewrites the upstream's
     path-absolute `Location` back under the `/api/whep/` prefix before it
     reaches the browser — `whep.ts` itself needs no change, it already
     correctly resolves whatever `Location` it's given per spec.
   - `APID_WEBRTC_PUBLIC_BASE` (env var read by `internal/apidconfig`,
     used by `internal/apid/orchestrator.go`'s `GetLiveURLs` to build
     `{base}/{id}/whep`) changes from the absolute `http://localhost:8889`
     to the relative path `/api/whep` in `docker-compose.yaml`,
     `deploy/docker-compose.release.yaml`, `e2e/docker-compose.e2e.yaml` —
     `apidconfig.validate()` only checks non-empty, no scheme/host format
     is enforced, and `fmt.Sprintf("%s/%d/whep", base, id)` works
     identically with a relative base. This also makes the value
     domain-agnostic, matching how `web/src/api/apid.ts`/`farcd.ts` already
     hardcode relative bases (`/api/apid` etc.) rather than a
     server-supplied absolute URL.
   - **Found during implementation**: `new URL(location, url)` in
     `whep.ts` requires its `url` argument to already be absolute — once
     `APID_WEBRTC_PUBLIC_BASE` became relative, the existing code threw on
     every successful connection. Fixed by resolving `url` against
     `window.location.href` first; see `issues/03`'s "Correction found
     during implementation".
   - **Found during implementation**: since the browser no longer talks
     to mediamtx's WebRTC port directly in the release/e2e topologies
     (only nginx's container does, over the internal compose network),
     `8889:8889`'s host port publish in `deploy/docker-compose.release.yaml`
     and `e2e/docker-compose.e2e.yaml` became dead exposure and was moved
     under `expose:` (same treatment the control-API port `9997` already
     had). Left published in the root dev `docker-compose.yaml`, where a
     host-run `vite dev` session (not the containerized `web` service)
     still needs to reach it directly, same as farc/hlsd/apid's ports
     there.

## Issues

- `issues/01-live-row-icon-and-order.md` — icon-only archive button, moved
  to the front of the row; `bootstrap-icons` dependency.
- `issues/02-live-status-not-updating.md` — `LivePage` subscribes to
  `subscribeJournal` for `recording`/`last_connect_error`, mirroring
  `ChannelsIndexPage`'s existing pattern.
- `issues/03-whep-proxy-through-nginx.md` — WHEP signaling proxied through
  nginx (`/api/whep/`) instead of direct browser→mediamtx, with the
  `proxy_redirect` `Location`-rewrite gotcha and the
  `APID_WEBRTC_PUBLIC_BASE` config change.
- `issues/04-live-tile-raw-error-shown.md` — `LiveVideoTile` shows "нет
  сигнала" on any WHEP failure instead of the raw thrown error.
- `issues/05-stale-checked-channel-after-delete.md` — `LivePage` prunes
  checked ids for channels that no longer exist, fixing a stale grid
  layout after deleting a channel.
