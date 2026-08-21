# Rename hls_server binary/service to hlsd

Status: fixed (2026-08-21, via `/mattpocock-skills:tdd`)

See `.scratch/hlsd-rename/spec.md` for the full design conversation this
was split from.

## Goal

`internal/hlsd` (the process-wiring package for `hls_server`) is already
correctly named. Rename the binary/service/command itself from
`hls_server` to `hlsd` so the name matches everywhere, aligning with the
project's `<name>d` convention (`farcd`, and the new `apid` from
`.scratch/live-page/`).

## Scope — grep for `hls_server` repo-wide (excluding `.git/`) and update
every live reference to the binary/service/command name. Confirmed via
`grep -rl "hls_server" .` at design time; categories to handle:

- **Binary/command**: `cmd/hls_server/` → `cmd/hlsd/` (including
  `cmd/hls_server/main.go`, `cmd/hls_server/commands/*.go` — the cobra
  `Use:` string moves from `"hls_server"` to `"hlsd"`).
- **Build**: `taskfile.yaml` (`build/hls_server` task and its output
  binary name), `Dockerfile.hls_server` → `Dockerfile.hlsd` (or however
  the rename is best reflected — check `docker-compose.yaml`/
  `deploy/docker-compose.release.yaml`/`e2e/docker-compose.e2e.yaml`'s
  build context + image/service names too), `.dockerignore`.
- **CI**: `.github/workflows/ci.yml`, `.github/workflows/release.yml`.
- **Deploy/observability**: `deploy/docker-compose.release.yaml`,
  `deploy/observability/prometheus.yml`, `deploy/observability/
  promtail-config.yaml`, and the Grafana dashboard JSON files under
  `deploy/observability/grafana/dashboards/` (job/service labels).
- **Web**: `web/nginx.conf` (upstream/service name), `web/src/api/hls.ts`
  (only if it names the service in a comment/constant — the `/api/hls`
  route prefix itself is a web-app-internal convention, not the binary
  name, and does not need to change unless it explicitly says
  "hls_server" somewhere).
- **Go internal packages that mention `hls_server` in comments** (not
  their own package name, which is already `hlsd`/`hlsapi`/`hlsclient`
  etc.): `internal/api/catalog.go`, `internal/api/eventpush_test.go`,
  `internal/farcd/farcd_test.go`, `internal/hlsapi/*.go`,
  `internal/hlsclient/hlsclient.go`, `internal/hlsconfig/*.go`,
  `internal/hlsd/*.go`, `internal/playlist/playlist.go`,
  `internal/segmentcache/cache.go`, `internal/toccache/cache.go`,
  `internal/tocindex/*.go` — update doc comments that name the process,
  leave package names as-is.
- **Tests**: `e2e/tests/player-gap-skip.spec.ts`, `tests/e2e_test.go` —
  wherever they invoke/reference the binary by name.
- **Docs**: `README.md`, `CONTEXT.md`, `docs/mkdocs.yaml`,
  `docs/agents/domain.md`, `docs/docs/archive/02-storage.md`,
  `docs/docs/archive/11-service-composition.md`,
  `docs/docs/archive/12-hls-server.md` (consider whether the *filename*
  should also move to `12-hlsd.md` — check the numbered-doc convention
  before renaming files, since other docs may link to it by filename),
  ADRs `018`–`021` (their content references `hls_server` by name; ADR
  numbers/filenames themselves should NOT change — ADRs are immutable
  historical records, only prose mentions of the old name inside them
  may need a note, not a rewrite of the decision itself).
- `PLAN.md` — update to reflect the new binary name.

## Explicitly out of scope

- Rewriting historical `.scratch/*/spec.md` and `.scratch/*/issues/*.md`
  files from already-resolved feature work (e.g. `player-redesign`,
  `hls-toc-bootstrap`, `remove-msm-integration`, `dependency-upgrade`,
  etc.) — these are historical records of design conversations that
  happened when the binary was still called `hls_server`; leave them as
  they are.
- ADR content/decisions themselves (`018`–`021`) — only cosmetic name
  mentions, not the recorded decisions, are affected by this rename.

## Comments
