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
- [x] Phase 22 — `fblock-live`/`fblock-status` pages: render one fblock's media tree GNU-`tree`-style (byte-holding nodes show size, scalar nodes show value). Since the TOC only exists once at fblock finalization, `fblock-status` reads it via a new `GET /storages/{id}/fcontainers/{uuid}/tree?node=&offset=&limit=` (decoded, paginated JSON — `frames(video)`/`frames(audio)` can have millions of children, so it's lazy/paginated rather than dumping a whole tree). `fblock-live` shows the *currently recording* segment (which lives in-memory as a `*fcontainer.Filler`, well before any disk write): `internal/farcd`'s new ~1s ticker calls `Filler.ElementsSince`/`CapturePolicy.LiveElementsSince`/`IngestManager.LiveElementsSince` and pushes deltas as a new `"live"` WS frame (`EventPushServer.PublishLiveProgress`) scoped by `subscribeMessage.Channels` (till now unused in the global feed); the page aggregates individual frame nodes into a single growing counter and flashes structural nodes green for a few seconds. Frontend: `web/src/components/FblockTree.tsx` (shared renderer, lazy-fetch status mode / pushed-delta live mode), `web/src/api/fblocktree.ts`, `subscribeLive` in `events.ts`. Follow-ups from watching it against real RTSP media: (1) fixed a real, previously-latent bug the live tree made visible for the first time — `internal/ingest/rtsp.go`'s H264/H265 `OnPacketRTP` callbacks marked `changed=true` on every SPS/PPS/VPS sighting with no byte comparison against the previous value, so a camera that (commonly) re-announces byte-identical parameters before every IDR opened a spurious new `config(video)` version per GOP; now guarded with `bytes.Equal`, covered by `TestChannelIngest_RepeatedSPSPPSDoesNotDuplicateConfig`. (2) `fblock-status` replaced its manual storage+uuid form with a browsable table (new `GET /storages/{id}/fblocks`, backed by `unit.Index().Snapshot()` with no channel filter — `internal/api/fblocks.go`, `listFblocks`/`parseFblocksJSON`). (3) `fblock-live` gained a fill-bar above the tree showing the current segment's fblock byte layout (prolog/catalog fixed-format sections, growing content, an estimated TOC size via the existing `toc.ComputeOffsets`, epilog) — `Filler.ContentBytes` threaded through `LiveElementsSince`/`PublishLiveProgress`, rendered by `web/src/components/FblockFillBar.tsx` (flexbox layout, not width%, since the fixed sections round to 0px at real FblockSize scale otherwise). Second round of follow-ups: (4) the fill bar's toc segment was rendered as a fixed-width landmark like prolog/catalog/epilog, so it never visibly grew even though `estimated_toc_bytes` did — unlike those three (truly constant for a given Geometry), toc's real size grows through a recording, so it now combines a minimum `flexBasis` with `flexGrow: estimatedTocBytes`, same idea as the content zone. (5) `fblock-status`'s table (with pagination, `?offset=&limit=`, `GET /storages/{id}/fblocks` now returns `{total, fblocks}`) moved to its own page (`FblockListPage.tsx` at `/fblocks`); `fblock-status` itself is now a pure detail view (`?storage=&uuid=`, no table/picker), reached only via the list's "Open" button or `fblock-live`'s "предыдущий fblock →" link. (6) `fblock-live` lost its channel picker — it now shows every channel of the selected storage at once (one `<FblockTree mode="live">` per channel, `subscribeLive` upgraded from one channel to a `channels: number[]` list). Implementing this surfaced a genuine pre-existing bug: `fblock.created`/`fblock.ready` (`internal/farcd/farcd.go`'s `bridgeFblockEvents`) are storage-level events with no `Channel` field at all (a physical fblock write isn't attributable to one channel), so the old page's `if (e.channel !== channel) return` gate silently discarded every one of them — the live tree never actually reset on segment rollover, and the "previous fblock" link only ever came from the one-time `candidates()` bootstrap on mount. Fixed by dropping that dead channel-gate (there's one shared "previous fblock" link per storage now, updated directly from `fblock.ready`) and detecting each channel's own segment rollover client-side instead, from `LivePushMessage.total` dropping (each channel's `Filler` restarts its element counter near zero when its segment closes and a new one opens — the one per-channel signal that *is* real). Third round: (7) the user rejected round 2's N-cards-per-channel display as a workaround, not the real fix — ADR-014 already states multiple/all channels of a storage sharing one fcontainer is "реальный режим эксплуатации, а не крайний случай", and the storage layer (`fcontainer.Filler`, `Unit.WriteFcontainer`) already fully supports and tests this; only `internal/ingest` was missing the coordination. Added `internal/ingest/segment.go`'s `sharedSegment`, one per storage: `CapturePolicy` no longer owns a private `Filler` (its own continuous/event/prerecord/postrecord decision logic is unchanged) — it attaches/detaches from its storage's shared segment instead, which alone decides when to actually flush to disk, since one channel stopping can no longer be allowed to cut off others still writing into the same buffer. Flush triggers: size (`~min_container_share × fblock_size`, checked after every append — `fblock/params.go`'s `MinContainerShare`, previously only a geometry-init invariant, now doing double duty as a live flush target too) and "last active channel of that storage just detached" (keeps the old single-channel responsiveness). A `generation` counter invalidates every other attached channel's cached `configID` across a flush it didn't itself trigger (`internal/ingest/policy.go`'s `writeFrameLocked` retry-once-on-`errStaleGeneration`) — the same class of bug as (1) above, one layer down. Live progress (`IngestManager.LiveElementsSinceStorage`, `subscribeMessage.LiveStorages`, `livePushMessage` dropping `Channel`) moved from channel- to storage-scoped to match, and `fblock-live` reverted to exactly one tree per storage (round 2's per-channel cards deleted — no longer needed once the underlying data genuinely is one tree). `docs/docs/archive/10-capture-policy.md` §4.1 (new) documents the shared buffer and its flush triggers; §2/§4 amended to stop implying one buffer per channel.

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
GET    /storages/{id}/fblocks?offset=&limit=       -> {total,fblocks:[{index,state,uuid?,begin?,end?,protected,channels?}]} (Phase 22, whole-storage, no channel filter, paginated)
GET    /storages/{id}/fcontainers/{uuid}/tree?node=&offset=&limit= -> {node,children,offset,total} (Phase 22, decoded media tree)
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
| `web/src/api/events.ts` | Phase 18: `subscribeJournal`, an auto-reconnecting WS client for the Journal feed. Phase 22: `subscribeLive` (also dispatches `"live"` frames, scoped to a caller-supplied `channels: number[]` list so a page can cover a whole storage, not just one channel) |
| `web/src/api/fblocktree.ts` | Phase 22: `getFblockTreeNode`, typed client for the paginated decoded-tree endpoint |
| `web/src/pages/storages/{StoragesIndexPage,StorageNewPage,StorageEditPage}.tsx` | Rails-style split: list-only index + per-row "Edit" link; create form; edit page (retention/write_mode patch) |
| `web/src/pages/channels/{ChannelsIndexPage,ChannelNewPage,ChannelEditPage}.tsx` | Storage-filtered list + remove/trigger/start-stop-recording actions; create/edit forms with a storage `<select>` |
| `web/src/pages/PlayerPage.tsx` | Storage+channel+time-range form → candidates list → protected toggle + hls.js playback |
| `web/src/pages/JournalPage.tsx` | Phase 18: live event table, connect/reconnect status, client-side "Clear" |
| `web/src/pages/FblockLivePage.tsx` | Phase 22: shows the selected storage's one shared live-recording fcontainer (no channel picker — every channel currently writing into it appears as its own branch in a single `FblockTree.tsx` tree) |
| `web/src/pages/FblockListPage.tsx` | Phase 22 follow-up: paginated (100/page) browsable table of a storage's fblocks, split out of `FblockStatusPage.tsx`; "Open" navigates to it |
| `web/src/pages/FblockStatusPage.tsx` | Phase 22: pure detail view of one finalized fblock's tree (`?storage=&uuid=`), rendered by `FblockTree.tsx` |
| `web/src/components/FblockTree.tsx` | Phase 22: shared GNU-`tree`-style renderer — lazy/paginated fetch (status mode) or WS-pushed delta (live mode), frame-role aggregation, green new-node highlight |
| `web/src/components/FblockFillBar.tsx` | Phase 22: one fblock's byte-layout fill bar (prolog/catalog/data/toc/epilog), flexbox-based so the fixed-format sections stay visible at real FblockSize scale |
| `web/src/App.tsx` | `react-router-dom` shell: `/storages`, `/channels`, `/player`, `/journal`, `/fblock-live`, `/fblock-status` |
| `web/nginx.conf` | `/api/farcd/` → farcd, `/api/hls/` → hls_server, `/segments/` → hls_server, `/api/events/` → farcd WS, SPA fallback to `index.html` |
| `web/Dockerfile` | multi-stage: `node:22-alpine` build → `nginx:1.27-alpine` serve |
| `Dockerfile.farc`, `Dockerfile.hls_server` | multi-stage: `golang:1.25.3-bookworm` build (`CGO_ENABLED=0`) → `debian:12-slim` runtime |
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
- `internal/api/treejson.go` (Phase 22) — `decodeScalarValue`/`handleReadTree`, the finalized-fblock decoded-tree endpoint; `internal/api/eventpush.go`'s `liveNode`/`livePushMessage`/`PublishLiveProgress` reuse `decodeScalarValue` for the live-progress WS frame.
- `internal/fcontainer/filler.go` (`ElementsSince`, `ContentBytes`), `internal/ingest/policy.go` (`CapturePolicy.LiveElementsSince`), `internal/ingest/ingestmanager.go` (`IngestManager.LiveElementsSince`), `internal/farcd/farcd.go` (`liveCursors`, `runLiveProgressTicker`/`tickLiveProgress`) — Phase 22's live-progress pipeline, from the in-memory `Filler` a channel's `CapturePolicy` is still writing to, up to the periodic WS push.
- `internal/api/fblocks.go` (Phase 22) — `handleListFblocks`, the whole-storage fblock table endpoint; `parseFblocksPaging`/`fblockListResponse` (Phase 22 follow-up) add `?offset=&limit=` and the `{total, fblocks}` envelope.
- `internal/ingest/rtsp.go`'s `setupH264`/`setupH265` — the `bytes.Equal` guards (Phase 22 follow-up) that stop repeated-but-identical SPS/PPS/VPS from opening a spurious new `config(video)`/`config(audio)` version.
- `internal/ingest/segment.go` (Phase 22, third follow-up round) — `sharedSegment`, the per-storage coordinator multiple channels' `CapturePolicy` instances now share instead of each owning a private `*fcontainer.Filler`; owns the flush-trigger decision (size + last-channel-detached) and the `generation` counter that invalidates other channels' cached `configID`s across a flush they didn't trigger themselves.
- `internal/ingest/policy.go`'s `writeFrameLocked`/`ensureConfigLocked` — the retry-once-on-`errStaleGeneration` dance that makes sharing a segment safe across concurrent channels.
- `internal/storage/unit.go`'s `MinContainerShare()` — public accessor `internal/farcd` uses to size `sharedSegment`'s flush target off the storage's own Geometry/Params.
- `web/src/pages/FblockLivePage.tsx` (Phase 22, third follow-up round) — back to one `LiveTreeState`/one `<FblockTree mode="live">` for the whole storage, no per-channel state at all; `fblock.created`/`fblock.ready` are now genuinely storage-scoped events matching the shared segment 1:1, so they're sufficient on their own for reset/prevUUID (no more client-side "total dropped" heuristic from round 2).
- `internal/ingest/ingestmanager.go`, `internal/ingest/policy.go` — `List`/`AddChannel`/`RemoveChannel`/`Policy()`/`SetOnRecordingChange`, the runtime primitives the HTTP handlers and Journal events are built on.
- `internal/hlsapi/server.go` — exact hls_server route shapes.
- `internal/config/config.go`, `internal/hlsconfig/config.go` — exact JSON shapes for `deploy/*.config.json`; both have `Save`/`Duration.MarshalJSON`.
- `internal/farcd/farcd.go` (`persistNewStorage`, `persistNewChannel`/`persistUpdatedChannel`/`persistRemovedChannel`, `bridgeFblockEvents`) — Gaps 1/2/3/5's fixes plus the Journal's `f.push.Publish` calls.
- `internal/hlsd/hlsd.go` (`reconcile`/`reconcileOnce`/`applyRemoteList`/`startChannel`/`stopChannel`, `newCache`/`newS3Client`) — Gap 5's client-side reconciliation loop (single-goroutine ownership of `tracked`, no mutex, by design — ADR-021); cache-backend selection (Phase 19) lives in `newCache`.
- `internal/segmentcache/{cache,disk,s3}.go` — Phase 19's `backend` interface plus disk/S3 implementations; `Cache`'s public `Get`/`Put` is unchanged, so `internal/hlsapi/handlers.go` needed zero changes.
- `web/src/api/events.ts`, `web/src/pages/JournalPage.tsx` — Phase 18's client side.
- `cmd/farc/commands/index.go`, `cmd/hls_server/commands/index.go` — `-c/--config` flag both Dockerfiles' `CMD` must match.
- `taskfile.yaml` — `build/app`/`build/hls_server`/`build/web` tasks; per `CLAUDE.md`, leave the unrelated stale tasks (`run`, `help`, `db/*`, `env/*`) untouched.
- `.golangci.yaml`, `.github/workflows/{ci,release}.yml`, `deploy/docker-compose.release.yaml` — Phase 21's lint gate and CI/release pipeline. There is no `VERSION` file — `release.yml` derives the current version by reading the latest `v*.*.*` git tag.
