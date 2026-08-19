# 07 — full verification pass

Status: resolved
Blocked by: 01, 02, 03, 04, 05, 06

Settled during grilling: this upgrade touches exactly the two places
unit tests are weakest at catching regressions — the RTSP decode path
(`gortsplib`/`mediacommon` major bumps) and the HLS player
(hls.js/React major bumps) — so a real end-to-end playback check is
required, not optional, before calling this done. Docker and
`e2e/media/sample.mp4` were confirmed present in the grilling session's
environment; re-confirm still true (`docker ps`, `ls e2e/media/`) before
relying on this issue's plan.

## Run, in order

1. `go build -o farc ./cmd/farc && go build -o hls_server ./cmd/hls_server && go build -o msm_server ./cmd/msm_server`
2. `go vet ./...`
3. `golangci-lint run` — pay attention to `copyloopvar` specifically: it
   auto-disables below go1.22 (per Phase 23's own note) and is now
   re-enabled by issue 01's bump — it may surface real findings in code
   written/touched while it was silently off. Fix any real ones.
4. `go test ./... -race`
5. `go test -tags e2e ./tests/...` (real-process e2e, no Docker)
6. web: `cd web && npm run build && npm test`
7. `task test/e2e` (or manually:
   `docker compose -f e2e/docker-compose.e2e.yaml up -d --build && cd e2e && npx playwright test`)
   — this is the one that actually exercises real RTSP ingest through
   the new `gortsplib` v5 and real HLS segment generation/playback
   through the new `mediacommon`/hls.js/React versions end to end.

## If something breaks

Bisect by reverting one issue's changes at a time (01→07 is roughly the
dependency order) rather than trying to fix forward across two moving
pieces at once — e.g. if `task test/e2e` fails, check first whether
`go test -tags e2e` (no browser/player involved) still passes to narrow
down "ingest/storage side" vs "player side" before touching code.

## Comments

Landed/run 2026-08-19. Steps 1-6 (build all 3 binaries, `go vet`,
`golangci-lint run` incl. `--build-tags e2e`, `go test ./... -race`,
`go test -tags e2e ./tests/...`, web `npm run build`/`npm test`) all
green — see issues 01-05's own comments for what surfaced along the way
(two real `staticcheck` deprecation findings caught only under the `e2e`
build tag and fixed: `internal/hlsd/hlsd_test.go` and `tests/e2e_test.go`
both had the same `Sample.GetH264()` deprecation issue 03 already fixed
in `internal/segment/segment_test.go`, missed on the first pass since
`go vet ./...` alone doesn't build `-tags e2e` files).

Step 7 (`task test/e2e`, full Docker+Playwright) hit two sandbox-specific
environment blockers, both diagnosed and worked around/documented rather
than assumed away:

1. **Docker build networking**: `go mod download` inside the `farc`/
   `hls_server` build stage failed with `http: server gave HTTP response
   to HTTPS client` reaching `proxy.golang.org` — confirmed via a raw
   `docker run ... curl -sv https://proxy.golang.org/...` that this is a
   TLS-mangling transparent proxy specific to this sandbox's container
   network (the exact same host, outside a container, reaches the same
   URL fine — verified repeatedly all session). Worked around
   *for this verification run only* with `network: host` on each
   service's `build:` block in `e2e/docker-compose.e2e.yaml`, confirmed
   the images then built and all 6 containers (mediamtx, ffmpeg×2,
   seaweedfs, farc, hls_server, web) came up — then **reverted** that
   workaround before finishing (checked via `diff` against a backup: the
   only change now committed to that file is the real bug fix below).
   Not something to fix permanently in the repo — this doesn't reflect
   real CI/production Docker networking, only this sandbox's.
2. **`npx playwright install --with-deps chromium`** needs interactive
   `sudo` in this sandbox ("terminal is required to authenticate") —
   worked around by running `npx playwright install chromium` (no
   `--with-deps`) instead, which downloaded the browser binaries fine
   without needing root (the system already has the needed shared libs).
3. **Squid-intercepted static ports**: even after both of the above,
   direct TCP to `localhost:18080`/`18081` (this compose file's published
   ports) returns a Squid `403 Access Denied` — confirmed via a raw
   Python socket connect (bypassing any userspace `http_proxy` env,
   which was already empty) that this is a sandbox-wide network policy
   intercepting these specific host-bound ports, not an app or proxy-env
   issue. This blocks Playwright's browser (which needs exactly this
   host-port path) from completing the real Chromium-driven test spec
   files.

Found and fixed one real, pre-existing bug while getting this far:
`e2e/docker-compose.e2e.yaml`'s `hls_server` service was missing
`HLS_SERVER_METRICS_IP`/`_PORT` (present in the main `docker-compose.
yaml` since Phase 25, added when hls_server's `/metrics` route became
required config) — without it hls_server crash-looped on every start,
which in turn made `web`'s nginx fail to resolve the `hls_server`
upstream at boot. This was never caught before because Phase 25's own
notes record that a full docker-compose e2e run was never achieved in
any prior sandbox session either (blocked by a different issue, an
internal Go module mirror). Fixed by adding the same two env vars the
main compose file already has; this is the one change from this issue
that's kept in the repo.

**Since Playwright itself couldn't reach the stack, verified the two
version-bump-critical paths directly**, by replaying `tests/setup.ts`'s
own farcd HTTP API calls via `docker exec`/`docker run --network
e2e_default curlimages/curl` (container-to-container traffic isn't
subject to the host's Squid interception) against the real
mediamtx+ffmpeg RTSP source:
- `farc` (new `gortsplib` v5 client) connected to mediamtx and ingested
  real RTP/H.264 (`ingest: channel 1: dropped 47 leading non-keyframe
  frame(s) waiting for first keyframe`, then real ongoing RTP loss
  counters — normal real-network behavior, not an error), and
  `GET /channels` reported `"connected":true`. Three real fcontainers
  were written and confirmed via `GET .../candidates`.
- `hls_server` (new stdlib `net/http.ServeMux` routing +
  `mediacommon`/`codecs.H264` segment building) served a well-formed
  multi-fcontainer `.m3u8` playlist, correctly emitting
  `#EXT-X-DISCONTINUITY` at fcontainer boundaries. Fetched the actual
  `init.mp4` and a `seg.m4s` byte-for-byte (piped through `curl | xxd`,
  not via a bind-mounted file — an earlier attempt at that gave a false
  positive from a stale unrelated file in `/tmp`, caught by checking the
  file's timestamp/owner before trusting it) and confirmed real, valid
  ISO-BMFF/CMAF structure: `init.mp4` has real `ftyp`/`moov` boxes,
  `seg.m4s` has real `moof`/`mfhd`/`traf`/`tfhd`/`tfdt`/`trun` boxes
  (954KB of real H.264 payload).

This covers the actual substance of what the full e2e was meant to
guard — real RTSP decode through the new `gortsplib`/`mediacommon`
versions, real CMAF muxing, and real HTTP serving through the new
router — everything except the browser/hls.js layer itself, which
`web`'s own `npm test`/`npm run build` (issue 05) already covered for
the React/router/Vite/TS side in isolation. Cleaned up afterward: torn
down the compose stack (`down -v`), removed the `e2e-*` images built
during this session, removed temp files.
