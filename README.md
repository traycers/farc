# farc

A system for archiving video/audio streams to disk using fblocks (fixed-size blocks) and serving the archive back over HTTP/HLS. `farc`/`farcd` itself is the archiving daemon; `hls_server` and the `web/` SPA build on top of it (see below).

## Components

- **`farc`** (`cmd/farc`, runs as the `farcd` daemon) — ingests RTSP streams and writes them to disk in fblocks; exposes an HTTP+WebSocket API for reading the archive back (`internal/api`).
- **`hls_server`** (`cmd/hls_server`) — a separate process that turns `farcd`'s archive into browser-playable HLS (H.264/AAC, VOD only, CMAF remux, no transcoding). Talks to `farcd` only through its external API.
- **`web/`** — a React SPA: an admin console for `farcd` (storages, capture policy) plus a VOD player backed by `hls_server`.

The design documentation (in Russian, under `docs/docs/archive/`) describes the full fblock/storage architecture; `PLAN.md` tracks the current implementation plan and known gaps for the web client and deployment.

## Build

Requires Go 1.25+. With [Task](https://taskfile.dev):

```sh
task build          # builds farc, hls_server, and web/dist
task build/app       # farc only
task build/hls_server
task build/web       # requires Node.js/npm
```

Or directly:

```sh
go build -o farc ./cmd/farc
go build -o hls_server ./cmd/hls_server
```

`task test` / `go test ./...` run the unit test suite. `tests/` holds an end-to-end test (build tag `e2e`) that runs both binaries as real OS processes:

```sh
go test -tags e2e ./tests/... -v
```

`task lint` runs `golangci-lint`. **Don't trust the rest of `taskfile.yaml`** — several tasks (`run`, `help`, `db/*`, `env/*`) were copied from an unrelated project and don't apply here.

## Run

Both binaries take a `-c`/`--config` flag pointing at a JSON config file (`internal/config`, `internal/hlsconfig`):

```sh
./farc -c farc.config.json
./hls_server -c hls_server.config.json
```

`farc`'s HTTP/WS/Metrics server addresses come from the environment instead of the JSON file — `FARC_HTTP_IP`/`FARC_HTTP_PORT`, `FARC_WS_IP`/`FARC_WS_PORT`/`FARC_WS_MAX_CONNECTIONS`, `FARC_METRICS_IP`/`FARC_METRICS_PORT` (`*_IP` defaults to `0.0.0.0`, `*_PORT` is required) — so a working deployment's env (or a `.env` file next to the binary, loaded via `godotenv`) can be committed alongside `docker-compose.yaml` without exposing per-site topology in `farc.config.json`. `farc.config.json` itself no longer ships as a repo file at all: if `-c` points at a path that doesn't exist yet, `farc` creates an empty one (`internal/config.EnsureExists`) rather than failing, so a fresh, empty config file/volume just works. Its `storages` list must reference already-initialized storage image files — `farcd` never creates them itself (the operator only needs to size a partition/file up front; `farcd` initializes it via `POST /storages`, e.g. through the web client's Storages page). `farcd` persists a newly created storage back into `farc.config.json` itself, so it's picked up again on the next restart — the config file must be writable by the process (see `docker-compose.yaml`'s `farc` service, which keeps it on a dedicated `farc_config` volume for exactly this reason).

`hls_server`'s HTTP address and segment/cache tuning are env-sourced the same way — `HLS_SERVER_HTTP_IP`/`HLS_SERVER_HTTP_PORT`, `HLS_SERVER_TARGET_SEGMENT_DURATION`, `HLS_SERVER_CACHE_DIR`, `HLS_SERVER_CACHE_QUOTA_BYTES` (`*_PORT`, `*_TARGET_SEGMENT_DURATION`, `*_CACHE_DIR` are required; `*_CACHE_QUOTA_BYTES` <= 0 means unbounded). `hls_server.config.json` keeps only the farcd endpoints it talks to and the channel → (farcd, storage) mapping — data hls_server never mutates itself, unlike `farc.config.json`'s storages/channels, so it stays a plain repo-tracked example config (`deploy/hls_server.config.json`).

## Docker / full stack

```sh
docker compose up --build
```

Brings up `farc`, `hls_server`, and `web` (nginx serving the SPA and reverse-proxying `/api/farcd/` and `/api/hls/` to the two backends) on `http://localhost/`. `farc.config.json` lives on the `farc_config` volume (bootstrapped empty on first start, then grown via the web client's Storages/Channels pages); `deploy/hls_server.config.json` is still a bind-mounted example config; archive/cache data lives in the `farc_data`/`hls_cache` named volumes.

## Docs

Design docs (Russian) build with [mkdocs-material](https://squidfunk.github.io/mkdocs-material/):

```sh
mkdocs serve -f docs/mkdocs.yaml
```
