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

`farc.config.json`'s `storages` list must reference already-initialized storage image files — `farcd` never creates them itself (the operator only needs to size a partition/file up front; `farcd` initializes it via `POST /storages`, e.g. through the web client's Storages page). `farcd` persists a newly created storage back into `farc.config.json` itself, so it's picked up again on the next restart — the config file must be writable by the process (see `docker-compose.yaml`'s `farc` service, which mounts it read-write for exactly this reason).

## Docker / full stack

```sh
docker compose up --build
```

Brings up `farc`, `hls_server`, and `web` (nginx serving the SPA and reverse-proxying `/api/farcd/` and `/api/hls/` to the two backends) on `http://localhost/`. Example configs live under `deploy/`; storage/channel data lives in the `farc_data`/`hls_cache` named volumes.

## Docs

Design docs (Russian) build with [mkdocs-material](https://squidfunk.github.io/mkdocs-material/):

```sh
mkdocs serve -f docs/mkdocs.yaml
```
