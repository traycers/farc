# web client (SPA) + deployment implementation plan

All phases below are complete (`[x]`). This file is the running summary of *what* changed and *why* per phase — for exact diffs, read the commit(s) for that phase in `git log`; this doc intentionally doesn't restate line-level detail already visible in the code.

## Progress

- [x] Phase 1 — scaffold `web/` (Vite + React + TS)
- [x] Phase 2 — `src/api` (typed farcd client + bigint-safe ns timestamps, `ns.ts`/`farcd.ts`)
- [x] Phase 3 — Storages page
- [x] Phase 4 — Channels page
- [x] Phase 5 — Player page
- [x] Phase 6 — routing/shell, `web/nginx.conf`, `web/Dockerfile`
- [x] Phase 7 — `taskfile.yaml` `build/web` task
- [x] Phase 8 — `Dockerfile.farc`, `Dockerfile.hls_server`, `docker-compose.yaml`, `deploy/` example configs
- [x] Phase 9 — `farcd` persists storages created via `POST /storages` back into its own config file (`config.Save`, `SetOnStorageCreated` hook) — closes Gap 3
- [x] Phase 10 — full channel CRUD (`GET/POST/PUT/DELETE /channels`), persisted the same way; `ChannelsPage` redesigned around it — closes Gaps 1/2
- [x] Phase 11 — restrict `hls_server` to exactly one `farcd` (ADR-020, `docs/docs/archive/adr/020-hls-server-single-farcd.md`). Follow-ups: moved `Farcd.HTTP`/`Farcd.WS` from the JSON config to `HLS_SERVER_FARC_HTTP`/`HLS_SERVER_FARC_WS` env vars; moved `hls_server.config.json` off a repo bind-mount onto a self-seeding Docker volume (`hlsconfig.EnsureExists`)
- [x] Phase 12 — `hls_server` reconciles its served-channel set against farcd's live `GET /channels` plus a global `channel.created`/`channel.removed` WS subscription, no restart needed (ADR-021, `docs/docs/archive/adr/021-hls-server-channel-reconciliation.md`) — closes Gap 5
- [x] Phase 13 — `hls_server` persists its reconciled channel list back into its own config file after every tracked-state change (`hlsconfig.Save`, `internal/hlsd`'s `persist`), mirroring what `farcd` already does (Phase 9/10)
- [x] Phase 14 — `web/` UI overhaul: default dark theme, storage creation auto-defaults fchunk size and gains a "Generate ID" button, `StoragesPage`/`ChannelsPage` split into Rails-style `index`/`new`/`edit` pages
- [x] Phase 15 — fixed the Player page showing a false-positive candidate that then hung playback forever: `GET /storages/{id}/candidates` gained an opt-in `confirm=true` param that TOC-checks each candidate before returning it; `PlayerPage` now requests it and surfaces hls.js errors instead of an infinite spinner
- [x] Phase 16 — fixed `hls_server`'s on-disk segment cache colliding across channels sharing one fcontainer: `segmentcache.Key` gained a `Channel` field. Existing pre-fix cache entries are silently orphaned — clear the `hls_server` cache dir once after upgrading
- [x] Phase 17 — real-media e2e harness (`e2e/`: mediamtx + looping ffmpeg + Playwright against a real headless browser). Found and fixed the actual root cause of a recurring `fragParsingError`: `web/nginx.conf` had no `/segments/` proxy route, so every segment request silently fell through to the SPA's `index.html`
- [x] Phase 18 — live "Журнал" event-log page over WebSocket: the per-channel `ChannelEvent` was generalized into `JournalEvent` with a much broader vocabulary (fblock created/deleted, recording started/stopped, recording command start/stop, trigger fired), served at `GET /events/ws`; `web/src/pages/JournalPage.tsx` consumes it. Also fixed a missing `/api/events/` WS proxy route in `web/nginx.conf` (dev-only in `vite.config.ts`, never mirrored into the config Docker actually serves)
- [x] Phase 19 — `hls_server`'s segment cache made backend-agnostic (disk, or any S3-compatible store via `aws-sdk-go-v2` — SeaweedFS/MinIO/S3/Ceph RGW), removing the local non-rebuildable state that blocked running multiple stateless `hls_server` replicas. `docker-compose.yaml` gained an opt-in `seaweedfs` service (`profiles: [s3]`); disk stays the default, zero behavior change for existing deployments
- [x] Phase 20 — fixed a real reallocation inefficiency in `internal/ingest/rtsp.go`'s `muxAnnexB` (pre-sized `make` once instead of repeated grow-and-copy `append` from `nil`, ~2-3 avoidable reallocations per keyframe access unit). A live555-style "checkout/return to a buffer pool" pattern was considered for the decoded-frame path and rejected: unlike live555's single-use network buffers, a decoded frame is retained by `CapturePolicy`'s `FrameQueue` for the channel's whole retention window with a variable, wall-clock-bounded lifetime — safe pooling would need cross-owner reference counting for an uncertain payoff
- [x] Phase 21 — GitHub Actions: `.golangci.yaml` expanded from 2 linters to ~28 (fixed ~250 real findings this surfaced); `ci.yml` runs `test`/`lint`/`e2e-real-process`/`web-build`/`docker-build` on every push/PR to `main`; `release.yml` triggers on both a direct push to `main` and a PR merged into `main` — since merging a PR via the GitHub UI *also* pushes the merge/squash/rebase result to `main`, a `determine` job tells the two apart per-event via the GitHub API (whether the pushed commit is already associated with a merged PR) rather than guessing from the commit message, so a merge never double-releases. Versioned CalVer-style, `YYYY.MM.N` (git tag `vYYYY.MM.N`, Docker tag `YYYY.MM.N`), switched from an initial SemVer major.minor.patch scheme — `N` is a plain `git rev-list --count` of commits reachable from `HEAD` since the 1st of the current month, not a dependency on the previous tag's value, so there's no SemVer-style major/minor/patch judgment call left to make per commit. Publishes `farc-<version>.tar` (all three Docker images + a rendered `docker-compose.yaml`) as a GitHub Release. No registry push, no `--version` CLI flag or web UI version display — version is only ever the Docker image tag / git tag, by explicit choice. The new CI immediately caught two real, previously-latent bugs: an ADR-021 race in `internal/hlsd/hlsd.go`'s `reconcileOnce` (subscribed to the live event stream *after* the bootstrap list snapshot, leaving a window a channel-created-in-that-gap could miss for up to 30s) and a build-tag-only compile error in `tests/e2e_test.go` (6 `err := ...` sites redeclaring an already-declared `err`, invisible to a plain `go vet ./...` without `-tags e2e`). `actions/checkout`/`actions/setup-node` bumped `v4`→`v5` after GitHub deprecated Node.js 20 runners.
- [x] Phase 22 — moved hosting from the internal Bolid GitLab (`gitlab.rigel.bolid.ru/rigel/services/archive/farc`) to a public GitHub repo, `github.com/traycers/farc`. `origin` repointed with the full, real commit history intact (no squash, no rewrite) — `.github/workflows/ci.yml`/`release.yml` needed no changes, since they were already GitHub-native and had been the live CI/release pipeline all along (no GitLab CI ever existed in this repo, and `main` has always been the branch both workflows trigger on). Follow-up cleanup once the repo was public: Go module path renamed `gitlab.rigel.bolid.ru/rigel/services/archive/farc` → `github.com/traycers/farc` across `go.mod` and all internal import paths; `docs/mkdocs.yaml`'s `site_url` updated to the new GitHub URL and `repo_url`/`repo_name` added for the docs theme's source link; the three service Dockerfiles and `web/Dockerfile` had their `FROM` lines switched from the internal-only registry mirror (`gitlab.rigel.bolid.ru:5050/mirror/...`, unreachable from public GitHub-hosted CI runners) to the same images pulled directly from Docker Hub, and `web/Dockerfile`'s unused `ARG NPM_MIRROR` (never actually wired into the `npm ci` call) was dropped as dead code.
- [x] Phase 24 — трейсинг по `X-Request-Id`/`X-Session-Id` (envoy, sitting in front of the whole system, sets these on inbound requests) plus file-based logging in all three binaries. New `internal/tracing` package: `Middleware(logf)` wraps any `http.Handler` (works uniformly on `mux.Router` and on `internal/api.EventPushServer`'s bare WS handler, which isn't routed through mux), reads both headers only if present (never fabricates an id), carries them on the request's `context.Context`, and logs one access-log line per request through the caller's existing `logf func(format string, args ...any)` — no change to that signature anywhere else in the codebase. Wired onto `farcd`'s `httpSrv`/`wsSrv` (`internal/farcd/farcd.go`, `metricsSrv` deliberately left unwrapped — internal scrape traffic, not proxied through envoy) and `hls_server`'s `httpSrv` (`internal/hlsd/hlsd.go`), both via a `func(format string, args ...any) { f.logf(format, args...) }` indirection — capturing `f.logf`/`h.logf` by value at server-construction time would have permanently bound to the no-op default, since `SetLogger` is only called by `cmd/farc`/`cmd/hls_server` after `New()` returns. Sequential trace: `hls_server`'s single outbound HTTP choke point (`internal/hlsclient/hlsclient.go`'s `do`) forwards the same headers onto its own calls to farcd, so one browser playback request shows the same `request_id` in both services' logs. Caught one real bug while wiring the WS server: wrapping `http.ResponseWriter` in a status-recording struct broke `internal/api.EventPushServer`'s `gorilla/websocket` upgrade, because embedding the `http.ResponseWriter` interface doesn't promote `http.Hijacker` (a separate interface) even when the concrete writer underneath implements it — fixed by forwarding `Hijack` explicitly, with a regression test (`internal/tracing/tracing_test.go`) that dials a real WS handshake through the middleware, since `httptest.NewRecorder` can't catch this (it doesn't implement `Hijacker` either way). File logging: `FARC_LOG_DIR`/`HLS_SERVER_LOG_DIR`/`MSM_SERVER_LOG_DIR` (all optional — empty means stderr only, unchanged from before) added to `internal/config`/`internal/hlsconfig`/`internal/msmconfig` following each package's existing env-parsing convention; `cmd/*/commands/default.go` open `<dir>/<service>.log` for append and log to `io.MultiWriter(os.Stderr, file)` via a plain `*log.Logger` — kept as three near-identical ~15-line blocks rather than a shared package, since the only difference is the service/env-var name. `log/slog` was considered and rejected: every long-lived component already logs through the string-formatted `logf` callback, and switching just the new access-log line to structured output while leaving everything else as-is would be an inconsistent half-migration for no real gain.
- [x] Phase 23 — downgraded `go.mod` from `go 1.25.3` to `go 1.21.0`. `github.com/bluenviron/gortsplib/v5` (every v5 release requires Go≥1.24, no exceptions) replaced with `github.com/bluenviron/gortsplib/v4 v4.13.1`, the last v4 release still on `go 1.21.0` — API is nearly identical; only `internal/ingest/rtsp.go`'s `NewClient` (v4's `*gortsplib.Client` has no `Scheme`/`Host` fields, `Start(scheme, host string) error` takes them explicitly instead of `Start()`) and `internal/ingest/rtsp_integration_test.go` (v4 has no `Server.NetListener()`; its `Server.Listen` hook is used instead to capture the real bound `:0` address) needed changes, confined to `internal/ingest/`. `github.com/bluenviron/mediacommon/v2` downgraded `v2.9.2` → `v2.1.0` (last release on `go 1.21.0`) — `pkg/formats/mp4/codecs` doesn't exist yet at that version (added in v2.6.0, which needs go1.24); its `codecs.H264`/`codecs.MPEG4Audio` become `fmp4.CodecH264`/`fmp4.CodecMPEG4Audio` directly, and `fmp4.PartTrack.Samples` is `[]*PartSample` rather than `[]*Sample` — `internal/segment/{init,media}.go` and its test updated accordingly. `aws-sdk-go-v2`/`config`/`credentials`/`service/s3` and their transitive tree (smithy-go, sso/ssooidc/sts, pion/rtp+rtcp+sdp, golang.org/x/net+x/sys) all repinned to the last go1.21-compatible generation each had. Separately (not a dependency issue): farcd's and hls_server's entire HTTP API (`internal/api/server.go`, `internal/hlsapi/server.go`, ~25 handler call sites) turned out to depend on Go 1.22's `http.ServeMux` method+`{param}` patterns and `r.PathValue` — ported to `github.com/gorilla/mux` (`mux.Vars(r)["id"]`), a go1.20-compatible router; route path syntax (`{id}`) is unchanged, only the method now comes from `.Methods(...)` instead of a `"METHOD /path"` string prefix. The downgrade also surfaced a real, previously-latent bug of its own: `internal/playlist/playlist.go`'s `Build` took `&rec` (a `for _, rec := range records` loop variable) as `prev` across iterations — silently correct only because Go 1.22 gives every iteration its own copy; under go1.21 semantics `prev` aliased the same reused variable as the current record, so `configChanged` always compared a record against itself and `#EXT-X-DISCONTINUITY` never fired on a real codec-config change. Fixed by indexing the backing slice (`&records[idx]`) instead of the loop variable — correct under either semantics, not just a go1.21 workaround. Note: golangci-lint's `copyloopvar` linter (which specifically catches this class of bug) disables itself below Go 1.22, so this class of mistake no longer gets caught automatically — worth double-checking any future `for range` loop that keeps a pointer alive past its iteration.
- [x] Phase 25 — observability: structured log levels, real Prometheus
  metrics in all three binaries, and a bundled Prometheus+Loki+Grafana
  stack (`.scratch/observability/spec.md`). New `internal/levellog`
  package wraps the existing `func(format string, args ...any)` logf
  callback with `.Info`/`.Warn`/`.Error` methods that just prepend
  `level=X ` to the format string — public `SetLogger` signatures are
  unchanged everywhere, so no call site outside the ~40 that needed a
  level assignment (plus `cmd/*/commands/default.go`'s own start/stop/
  fatal-config lines) was touched. Added `github.com/prometheus/
  client_golang` (pinned to v1.19.1, not `@latest`: a newer version's
  transitive deps pulled `go.mod`'s `go` directive back up to 1.25,
  undoing Phase 23's deliberate downgrade — v1.19.1 is the newest
  release that doesn't force that bump) to all three binaries, verified
  by actually building and running the observability half of
  `docker-compose.yaml` (Prometheus scraping real target configs, a
  real Promtail→Loki log pipeline queried live via LogQL, Grafana
  serving all 3 provisioned dashboards) — `farc`/`hls_server` couldn't
  be built and joined into that same live run in this instance's
  sandbox specifically because its internal Go module mirror
  (`golangmirror.bolid.ru`) only mirrors a curated, non-pull-through
  set of versions and is missing several of `client_golang`'s
  transitive deps at any version tried (confirmed independent of
  `client_golang`'s own version: both a newer chain — `klauspost/
  compress`, `munnerz/goautoneg`, newer `prometheus/{client_model,
  common,procfs}` — and an older one — `golang.org/x/sync`,
  `github.com/{matttproud/golang_protobuf_extensions,mwitkow/
  go-conntrack}` at the exact old floor versions pre-1.17 module-graph
  expansion needs — are absent); `go build`/`go test -race`/
  `golangci-lint` all stay green throughout via the public Go proxy.
  Left as a real open item for whoever runs this for real: either get
  ops to mirror the missing packages, or confirm GitLab CI's runners
  reach a proxy with broader coverage than this sandbox's. `internal/
  api/metrics.go`'s hand-rolled
  `writeUnitMetrics` became a `prometheus.Collector` (same metric
  names/labels, still computed live at scrape time, no history —
  `02-storage.md` §8's design unchanged); `hls_server`/`msm_server` each
  gained a new `/metrics` (config: `HLS_SERVER_METRICS_IP/PORT`,
  `MSM_SERVER_METRICS_IP/PORT`, both required like farcd's own) serving
  free `go_*`/`process_*` runtime collectors plus one new domain gauge
  each — `hls_server_connected_channels` (`internal/hlsd.Hlsd`'s
  already-single-goroutine-owned `tracked` map's length, mirrored into
  an `atomic.Int32` at its one mutation choke point, `persist`, for
  lock-free concurrent reads) and `msm_server_ws_connected`
  (`internal/msmd`'s WS-subscribe loop flips an `atomic.Bool` on
  connect/disconnect). `docker-compose.yaml`/`deploy/docker-compose.
  release.yaml` gained `prometheus`/`loki`/`promtail`/`grafana`
  services, always on (not an opt-in profile like `s3`/`msm`) since
  observability was the explicit point of the request; `promtail`
  reads every container's stdout/stderr straight off `docker.sock` via
  `docker_sd_configs` (no host-level log-driver plugin, no code
  change), `grafana` is provisioned entirely as code (`deploy/
  observability/`'s `datasources.yaml` + 3 dashboard JSON files —
  Storage & Fblocks, Services Overview, Logs — auto-loaded on first
  start, matching this repo's existing `config.EnsureExists`-style
  convention of avoiding manual setup).

## Context

`farc` (farcd) and `hls_server` are two complete, working Go binaries. This plan added a React SPA (`web/`) doubling as admin console (storage/channel management) and VOD player, Dockerfiles, a docker-compose stack, and — once the app itself stabilized — a GitHub Actions CI/release pipeline (Phase 21).

Scope decision (explicit, still current): full admin console, not just a player — Vite + React + TypeScript + hls.js, no state-management or UI component library.

## Real API surface the SPA is built against

**farcd (`internal/api`)** — admin/data plane:

```
POST   /storages                                  {id,path,geometry,params,force,catalog_path,backend} -> 201 {id,path,geometry}
GET    /storages                                  -> [{id,path,geometry}]
PATCH  /storages/{id}                              {retention_days?,write_mode?} -> 204
GET    /storages/{id}/candidates?channel=&t1=&t2=  -> [{index,uuid,begin,end}]
POST   /storages/{id}/fcontainers/{uuid}/protected {value:bool} -> 204
GET    /channels                                   -> [{channel,rtsp_url,storage,capture_policy_type,prerecord_ns,postrecord_ns}]
POST   /channels                                   {id,rtsp_url,storage,capture_policy:{type,max_deferred_start_ns,prerecord_ns,postrecord_ns}} -> 201
PUT    /channels/{id}                               {rtsp_url,storage,capture_policy:{...}} -> 200 (remove+re-add under the hood)
DELETE /channels/{id}                               -> 204
POST   /channels/{id}/capture-policy               {type,params:{prerecord_ns,postrecord_ns}} -> 204
POST   /channels/{id}/events                       {t?} -> 204
GET    /events/ws                                  (Journal WS feed, Phase 18 — global or per-storage subscription)
GET    /metrics                                    (Prometheus text; linked out, not parsed)
```

`resolve`, raw TOC/content routes (`GET .../toc`, `GET .../fcontainers/{uuid}`) are not used by the SPA — playback goes through `hls_server`, not through farcd's fallback-resolve path.

**hls_server (`internal/hlsapi`)** — 3 player-facing routes, all that exist:

```
GET /channels/{channel}/hls/{t1}/{t2}/playlist.m3u8
GET /segments/{channel}/{storage}/{uuid}/init.mp4
GET /segments/{channel}/{storage}/{uuid}/{n}/seg.m4s
```

## Package/file layout

| Path | Responsibility |
|---|---|
| `web/src/api/ns.ts` | `bigint` helpers for Unix-nanosecond timestamps: `nsFromDate`, `nsToDate`, `parseCandidatesJSON` |
| `web/src/api/farcd.ts` | Typed fetch client: `listStorages`, `createStorage`, `patchStorage`, `candidates`, `setProtected`, `setCapturePolicy`, `triggerEvent`, `listChannels`, `createChannel`, `updateChannel`, `removeChannel` |
| `web/src/api/events.ts` | Phase 18: `subscribeJournal`, an auto-reconnecting WS client for the Journal feed |
| `web/src/pages/storages/{StoragesIndexPage,StorageNewPage,StorageEditPage}.tsx` | Rails-style split: list-only index + per-row "Edit" link; create form; edit page (retention/write_mode patch) |
| `web/src/pages/channels/{ChannelsIndexPage,ChannelNewPage,ChannelEditPage}.tsx` | Storage-filtered list + remove/trigger/start-stop-recording actions; create/edit forms with a storage `<select>` |
| `web/src/pages/PlayerPage.tsx` | Storage+channel+time-range form → candidates list → protected toggle + hls.js playback |
| `web/src/pages/JournalPage.tsx` | Phase 18: live event table, connect/reconnect status, client-side "Clear" |
| `web/src/App.tsx` | `react-router-dom` shell: `/storages`, `/channels`, `/player`, `/journal` |
| `web/nginx.conf` | `/api/farcd/` → farcd, `/api/hls/` → hls_server, `/segments/` → hls_server, `/api/events/` → farcd WS, SPA fallback to `index.html` |
| `web/Dockerfile` | multi-stage: `node:22-alpine` build → `nginx:1.27-alpine` serve |
| `Dockerfile.farc`, `Dockerfile.hls_server` | multi-stage: `golang:1.21-bookworm` build (`CGO_ENABLED=0`) → `debian:12-slim` runtime |
| `docker-compose.yaml` | services `farc`, `hls_server`, `web`, optional `seaweedfs` (`profiles: [s3]`); only `web` publishes a host port |
| `deploy/docker-compose.release.yaml` | Phase 21: same topology as above, `build:` replaced with `image: <service>:__VERSION__`, rendered by `release.yml` |

## Gap resolutions

- **Gap 1 — no channel discovery — fixed in Phase 10.** `GET /channels` lists every running channel; `ChannelsPage` uses it directly.
- **Gap 2 — no GET for capture-policy — fixed as a side effect of Phase 10.** `GET /channels`'s policy fields read live off each channel's `CapturePolicy`, not a stale copy.
- **Gap 3 — `POST /storages` didn't persist into farcd's config — fixed in Phase 9.** `persistNewStorage` appends + `config.Save`s before the HTTP response; a save failure rolls back the in-memory append (the storage itself stays registered/usable — `storage.Init` already ran).
- **Gap 4 — nanosecond timestamps exceed JS safe-integer range.** Handled client-side (`ns.ts`); not a backend defect.
- **Gap 5 — `hls_server`'s channel list required a restart to pick up farcd changes — fixed in Phase 12.** ADR-021: farcd's live `GET /channels` + global WS subscription are the source of truth; the config file's `channels` list is only a bootstrap seed.

## Critical files

- `internal/api/{server,storages,channels,query,fcontainers,eventpush}.go` — exact farcd route/body/response shapes; `JournalEvent`/`EventPushServer.Publish`/the `serveGlobal` branch (Phase 18/12's wire protocol).
- `internal/ingest/ingestmanager.go`, `internal/ingest/policy.go` — `List`/`AddChannel`/`RemoveChannel`/`Policy()`/`SetOnRecordingChange`, the runtime primitives the HTTP handlers and Journal events are built on.
- `internal/hlsapi/server.go` — exact hls_server route shapes.
- `internal/config/config.go`, `internal/hlsconfig/config.go` — exact JSON shapes for `deploy/*.config.json`; both have `Save`/`Duration.MarshalJSON`.
- `internal/farcd/farcd.go` (`persistNewStorage`, `persistNewChannel`/`persistUpdatedChannel`/`persistRemovedChannel`, `bridgeFblockEvents`) — Gaps 1/2/3/5's fixes plus the Journal's `f.push.Publish` calls.
- `internal/hlsd/hlsd.go` (`reconcile`/`reconcileOnce`/`applyRemoteList`/`startChannel`/`stopChannel`, `newCache`/`newS3Client`) — Gap 5's client-side reconciliation loop (single-goroutine ownership of `tracked`, no mutex, by design — ADR-021); cache-backend selection (Phase 19) lives in `newCache`.
- `internal/segmentcache/{cache,disk,s3}.go` — Phase 19's `backend` interface plus disk/S3 implementations; `Cache`'s public `Get`/`Put` is unchanged, so `internal/hlsapi/handlers.go` needed zero changes.
- `web/src/api/events.ts`, `web/src/pages/JournalPage.tsx` — Phase 18's client side.
- `cmd/farc/commands/index.go`, `cmd/hls_server/commands/index.go` — `-c/--config` flag both Dockerfiles' `CMD` must match.
- `taskfile.yaml` — `build/app`/`build/hls_server`/`build/web` tasks; per `CLAUDE.md`, leave the unrelated stale tasks (`run`, `help`, `db/*`, `env/*`) untouched.
- `.golangci.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `deploy/docker-compose.release.yaml` — Phase 21's lint gate and CI/release pipeline, unchanged by Phase 22's move to GitHub hosting since they were GitHub-native from the start. `release.yml` derives the current version from `git rev-list --count` since the 1st of the month (CalVer `YYYY.MM.N`), not a `VERSION` file.
