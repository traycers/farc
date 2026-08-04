# web client (SPA) + deployment implementation plan

## Progress

- [x] Phase 1 — scaffold `web/` (Vite + React + TS)
- [x] Phase 2 — `src/api` (typed farcd client + bigint-safe ns timestamps)
- [x] Phase 3 — Storages page
- [x] Phase 4 — Channels page
- [x] Phase 5 — Player page
- [x] Phase 6 — routing/shell, `web/nginx.conf`, `web/Dockerfile`
- [x] Phase 7 — `taskfile.yaml` `build/web` task
- [x] Phase 8 — `Dockerfile.farc`, `Dockerfile.hls_server`, `docker-compose.yaml`, `deploy/` example configs
- [x] Phase 9 — `farcd` persists storages created via `POST /storages` back into its own config file (closes Gap 3)

## Context

`farc` (farcd) and `hls_server` (previous `PLAN.md`, now superseded by this one — see repo history) are two complete, working Go binaries with no web UI and no deployment artifacts. This plan adds a React SPA (`web/`) that doubles as an admin console (storage/channel management) and a VOD player, plus Dockerfiles and a docker-compose stack tying `farc`, `hls_server`, and the SPA together behind one nginx origin.

Scope decision (explicit): full admin console, not just a player — built with Vite + React + TypeScript + hls.js, no state-management or UI component library (three pages, one fetch layer, plain CSS).

## Real API surface the SPA is built against

**farcd (`internal/api`)** — admin/data plane:

```
POST   /storages                                  {id,path,geometry,params,force,catalog_path,backend} -> 201 {id,path,geometry}
GET    /storages                                  -> [{id,path,geometry}]
PATCH  /storages/{id}                              {retention_days?,write_mode?} -> 204
GET    /storages/{id}/candidates?channel=&t1=&t2=  -> [{index,uuid,begin,end}]
POST   /storages/{id}/fcontainers/{uuid}/protected {value:bool} -> 204
POST   /channels/{id}/capture-policy               {type,params:{prerecord_ns,postrecord_ns}} -> 204
POST   /channels/{id}/events                       {t?} -> 204
GET    /metrics                                    (Prometheus text; linked out, not parsed)
```

`resolve`, raw TOC/content routes (`GET .../toc`, `GET .../fcontainers/{uuid}`), and `/events/ws` are not used by the SPA — playback goes through `hls_server`, not through farcd's fallback-resolve path.

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
| `web/src/api/farcd.ts` | Typed fetch client: `listStorages`, `createStorage`, `patchStorage`, `candidates`, `setProtected`, `setCapturePolicy`, `triggerEvent` |
| `web/src/pages/StoragesPage.tsx` | List (`GET /storages`), create form, inline retention/write_mode patch |
| `web/src/pages/ChannelsPage.tsx` | Channel-id input, capture-policy form, trigger-event form |
| `web/src/pages/PlayerPage.tsx` | Storage+channel+time-range form → candidates list → protected toggle + hls.js playback |
| `web/src/App.tsx` | `react-router-dom` shell, routes `/storages` `/channels` `/player` |
| `web/nginx.conf` | `/api/farcd/` → farcd, `/api/hls/` → hls_server, SPA fallback to `index.html` |
| `web/Dockerfile` | multi-stage: `node:22-alpine` build → `nginx:1.27-alpine` serve |
| `Dockerfile.farc`, `Dockerfile.hls_server` | multi-stage: `golang:1.25.3-bookworm` build (`CGO_ENABLED=0`) → `debian:12-slim` runtime |
| `docker-compose.yaml` | services `farc`, `hls_server`, `web`; only `web` publishes a host port |
| `deploy/farc.config.json`, `deploy/hls_server.config.json` | example configs, bind-mounted into the containers |

## Build order and verification

1. **Scaffold `web/`.** `package.json` (react, react-dom, react-router-dom, hls.js, typescript, vite, `@vitejs/plugin-react`), `vite.config.ts`, `tsconfig.json`, `index.html`, `src/main.tsx`. Verify: `npm install && npm run build` produces `web/dist`.
2. **`src/api/ns.ts` + `src/api/farcd.ts`.** All timestamps as `bigint`; `parseCandidatesJSON` regex-quotes the `begin`/`end` integer literals before `JSON.parse` (native JSON parsing silently rounds nanosecond epoch values — they're already past `Number.MAX_SAFE_INTEGER`). Verify: a quick manual check that a known large literal round-trips exactly through `parseCandidatesJSON`.
3. **`StoragesPage`.** Table from `listStorages`; create form posts full `createStorage` body; inline `retention_days`/`write_mode` edits call `patchStorage` — `GET /storages` never echoes these back (`StorageInfo` only carries `id`/`path`/`geometry`), so the page just reports success/failure rather than reflecting a value it doesn't have.
4. **`ChannelsPage`.** Channel-id free-text input (Gap 1) feeding two independent forms: set capture-policy, trigger event. No "current policy" display (Gap 2) — UI copy says so explicitly rather than showing stale/fake data.
5. **`PlayerPage`.** Storage-id (populated from `listStorages`) + channel-id + `datetime-local` from/to → `candidates()`; render `{uuid, begin, end}` rows with a protected-toggle button and a "Play" button that points an `<video>` (hls.js) at `/api/hls/channels/{channel}/hls/{t1}/{t2}/playlist.m3u8`.
6. **Shell + nginx.** `App.tsx` routing; `web/nginx.conf` proxy rules; `web/Dockerfile`. Verify: `docker build ./web`.
7. **`taskfile.yaml`.** New `build/web` task (`dir: web`, `npm ci && npm run build`), wired into the aggregate `build` task.
8. **Root Dockerfiles + compose.** `Dockerfile.farc`/`Dockerfile.hls_server`, `.dockerignore`, `docker-compose.yaml`, `deploy/*.config.json`. Verify: `docker build -f Dockerfile.farc .`, `-f Dockerfile.hls_server .`; `docker compose config` validates the compose file.
9. **Persist storages to `farc.config.json`.** `internal/config.Save` (new: `json.MarshalIndent` + `os.WriteFile`, overwriting in place rather than temp-file-plus-rename — a rename would detach from a single-file Docker bind mount instead of updating the host-visible file). `internal/config.Duration` gained `MarshalJSON` (it only had `UnmarshalJSON` before; `Save` needs both directions or every duration field round-trips as a raw nanosecond number `Load` then rejects). `internal/api.HttpApiServer` gained `SetOnStorageCreated(func(id, path, catalogPath string) error)`, called by `handleCreateStorage` right after a successful `Register`, before the response is written; a nil/unset hook is a no-op (existing callers unaffected). `internal/farcd.New` now takes `configPath` alongside `cfg` and wires `Farcd.persistNewStorage` (mutex-guarded append to `cfg.Storages` + `config.Save`) as that hook — `docker-compose.yaml`'s `farc.config.json` mount is no longer `:ro`. Verify: `internal/config`'s `TestSave_RoundTripsThroughLoad`/`TestSave_OverwritesInPlace`; `internal/api`'s `TestHandleCreateStorage_CallsOnStorageCreatedHook`/`..._OnStorageCreatedErrorFailsRequestButKeepsRegistration`; `internal/farcd`'s `TestRun_CreateStorageOverHTTP_PersistsToConfigFile` (real HTTP POST, then `config.Load` the file back and confirm the entry survived).

## Gap resolutions

- **Gap 1 — no channel discovery.** Channels exist only in farcd's static config; there is no list/create route. `ChannelsPage` takes a raw channel-id input. Not fixed (no Go changes for this one — out of scope, see Critical files).
- **Gap 2 — no GET for capture-policy.** Only the setter exists. The UI never claims to show a "current" policy. Not fixed.
- **Gap 3 — `POST /storages` didn't persist into farcd's config — fixed in Phase 9.** `internal/farcd`'s `persistNewStorage` now appends the new entry to the in-memory `*config.Config` and calls `config.Save` before the HTTP response is written; a save failure rolls back the in-memory append and fails the request with 500 (the storage stays registered and usable for this process's lifetime regardless — persistence failing doesn't undo an already-completed `storage.Init`). Re-POSTing the same storage is still not a safe way to "repair" a missed persist: `storage.Init` with `force:false` returns `ErrAlreadyInitialized`, and `force:true` destroys the existing catalog.
- **Gap 4 — nanosecond timestamps exceed JS safe-integer range.** Handled client-side per the `ns.ts` design above; not a backend defect.

## Critical files

- `internal/api/{server,storages,channels,query,fcontainers}.go` — exact farcd route/body/response shapes this client mirrors; `server.go` also has `SetOnStorageCreated`.
- `internal/hlsapi/server.go` — exact hls_server route shapes.
- `internal/config/config.go`, `internal/hlsconfig/config.go` — exact JSON shapes for `deploy/*.config.json`; `config.go` also has `Save`/`Duration.MarshalJSON`.
- `internal/farcd/farcd.go` (`persistNewStorage`, `openStorage`) — Gap 3's fix and its remaining edge (a storage that predates this feature, or was created before, must still be added to the config manually).
- `cmd/farc/commands/index.go`, `cmd/hls_server/commands/index.go` — `-c/--config` flag both Dockerfiles' `CMD` must match.
- `taskfile.yaml` — existing `build/app`/`build/hls_server` tasks `build/web` mirrors; per `CLAUDE.md`, leave the unrelated stale tasks (`run`, `help`, `db/*`, `env/*`) untouched.
