# WHEP requests routed through nginx instead of directly to mediamtx's port

Status: fixed (2026-08-21, via /mattpocock-skills:tdd)

See `.scratch/live-page-fixes/spec.md` for the full design conversation
this was split from, including the real-mediamtx diagnosis that ruled out
mediamtx's own CORS/auth config as the cause (confirmed root cause: a
local proxy — a Squid instance on the reporting machine — intercepting the
direct cross-origin browser request to mediamtx's port and returning its
own `403` with no CORS header, before the request ever reaches mediamtx).

## Goal

`web/src/api/whep.ts` makes a direct cross-origin `fetch` from the browser
to `{APID_WEBRTC_PUBLIC_BASE}/{id}/whep` (mediamtx's own WebRTC port,
`http://localhost:8889` today) — unlike every other backend (farcd, apid,
hlsd), which the web app reaches through nginx on its own origin
(`/api/farcd`, `/api/apid`, `/api/hls`). Route WHEP the same way: same
origin, through nginx, so a local proxy/antivirus/browser-extension
configured around the *web app's* origin (the thing every user's machine
already has to permit for the app to work at all) also covers WHEP,
instead of leaving a second, separately-exposed port as a distinct attack
surface for the same class of interception.

## Scope

- **`web/nginx.conf`**: new location block, same prefix-strip pattern as
  the existing `/api/apid/`/`/api/hls/` blocks:
  ```
  location /api/whep/ {
      proxy_pass http://mediamtx:8889/;
      proxy_redirect / /api/whep/;
  }
  ```
  The `proxy_redirect` line is required, not optional: mediamtx's WHEP
  handler (`sessionLocation` in
  `internal/servers/webrtc/http_server.go`, confirmed against the pinned
  v1.20.0 source) returns a path-absolute `Location: /{path}/whep/{secret}`
  on a successful POST — no scheme/host. Without `proxy_redirect`, that
  header reaches the browser unchanged; `whep.ts`'s
  `new URL(location, url)` then resolves it as an absolute-path reference,
  which *replaces the entire path* of the proxied URL, not just the
  segment after `/api/whep/` — so the teardown `DELETE` would land on the
  bare origin root instead of back through the proxy to mediamtx. nginx's
  SPA fallback (`try_files ... /index.html`) would swallow that as a
  `200 text/html` response, which the abort handler's
  `fetch(...).catch(() => {})` in `whep.ts` discards silently — WHEP
  sessions would leak on mediamtx with zero visible symptom. `proxy_redirect`
  rewrites the upstream's path-absolute `Location` back under
  `/api/whep/` before the browser sees it, so `whep.ts` needs no code
  change at all — it already resolves whatever `Location` it's given
  correctly per spec.
- **`web/vite.config.ts`**: matching dev-server proxy entry, same shape as
  the existing `/api/apid`/`/api/hls` ones:
  ```
  '/api/whep': { target: 'http://localhost:8889', rewrite: (p) => p.replace(/^\/api\/whep/, '') },
  ```
  (mediamtx's WebRTC port is already published to the host in
  `docker-compose.yaml` for local dev, same as the other three services'
  ports vite proxies to.) Vite's dev proxy doesn't have an equivalent to
  nginx's `proxy_redirect`; check whether Vite's proxy config needs an
  explicit `configure`/`onProxyRes` rewrite for the `Location` header in
  dev mode, or whether `http-proxy`'s default behavior already handles a
  path-absolute redirect correctly for this case — verify by hand (open
  the Live page in dev mode, connect and then navigate away, confirm no
  orphaned session server-side) since this is exactly the kind of
  silent-failure gotcha that won't show up as a failing automated test.
- **`APID_WEBRTC_PUBLIC_BASE`** changes from `http://localhost:8889` to
  the relative path `/api/whep` in `docker-compose.yaml`,
  `deploy/docker-compose.release.yaml`, and `e2e/mediamtx.yml`'s
  companion `e2e/docker-compose.e2e.yaml` (wherever the env var is
  currently set to the absolute mediamtx address). No Go code change:
  `internal/apidconfig`'s `validate()` only checks the value is
  non-empty, and `internal/apid/orchestrator.go`'s
  `fmt.Sprintf("%s/%d/whep", base, id)` produces a correct relative URL
  either way.
- **Seam / tests**: `web/src/api/whep.test.ts` already exercises
  `connectWhep` against a fake `fetch`/`RTCPeerConnection` — add/adjust a
  case asserting the POST URL and the resolved teardown `resourceUrl` when
  `Location` is a path-absolute string and the request `url` itself is a
  relative `/api/whep/...` path (matching what production now sends),
  confirming the existing `new URL(location, url)` resolution behaves as
  intended once nginx's `proxy_redirect` is accounted for at the
  infra level (the unit test can't exercise nginx itself, only confirm
  `whep.ts`'s own resolution logic is correct given a rewritten,
  prefix-preserving `Location`).

## Correction found during implementation

The spec assumed `whep.ts`'s `Location`-resolution logic needed no change.
That assumption was wrong: `new URL(location, url)`'s second argument
(`url`) must itself already be an absolute URL — confirmed by direct test
(`new URL('/x', '/api/whep/1/whep')` throws `TypeError: Invalid URL`).
Once `APID_WEBRTC_PUBLIC_BASE` becomes the relative `/api/whep` (this
issue's own change, below), `url` reaching `connectWhep` is relative too,
so the existing code throws on every successful connection, regardless of
what nginx does to `Location`. Fixed in `web/src/api/whep.ts`: resolve
`url` against `window.location.href` before using it as the base for
`Location` resolution (`new URL(location, new URL(url, window.location.href))`).
Covered by a new `whep.test.ts` case using a relative `url` and asserting
the resolved `resourceUrl` lands on `window.location.origin` with the
`Location` value appended.

## Explicitly out of scope

- ~~Changing `whep.ts`'s own `Location`-resolution logic~~ — see
  "Correction found during implementation" above; one line did need to
  change, though the resolution *behavior* per the WHEP spec is unchanged.
- Diagnosing or fixing the specific local Squid configuration on the
  reporting machine — out of this repo's control; this issue removes the
  class of problem it caused, not that specific proxy install.
- Media transport (RTP/ICE over UDP) — unaffected, still direct
  browser↔mediamtx.

## Comments

Implemented: `web/vite.config.ts`'s new `/api/whep` entry doesn't rely on
`http-proxy`'s default behavior — added an explicit `configure` hook
(`proxy.on('proxyRes', ...)`) that rewrites a path-absolute `Location`
response header the same way nginx's `proxy_redirect` does, so dev mode
doesn't leak WHEP sessions either. Verified end-to-end: `npm run build`
(tsc -b + vite build) succeeds with the new proxy config and the
`bootstrap-icons` fonts bundled; `npm test` green (170/170, 4 new). Real
WebRTC connectivity through the new proxy chain is still only
manually/e2e verifiable, per the spec's original note — no automated test
in this repo exercises a live WHEP session end-to-end.
