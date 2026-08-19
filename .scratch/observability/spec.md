# Observability stack: Prometheus + Loki + Grafana

Status: fixed (2026-08-19, via `/mattpocock-skills:tdd`)

## Request

User: добавить логи, стек Prometheus + Loki + Grafana, с готовыми дашбордами
в Grafana.

## Facts gathered during grilling (2026-08-19)

- **Metrics today**: only `farcd` exposes `/metrics` (`internal/api/metrics.go`),
  hand-rolled Prometheus text exposition (no `client_golang` dependency —
  `go.mod` has no Prometheus/Loki/otel/zap/logrus/zerolog deps at all). Every
  metric carries a `storage="<id>"` label: `farc_fblocks_total` and per-state
  counts (`uninitialized`/`ready`/`writable`/`retained`/`protected`/`bad`/
  `in_progress`), `farc_write_queue_depth`/`_status`, `farc_writes_total`,
  `farc_write_verify_failures_total`, `farc_reads_in_progress` (hardcoded 0,
  unused today), `farc_storage_state`, `farc_channel_registry_used`/
  `_capacity`. This is already documented as intentional in
  `docs/docs/archive/02-storage.md` §4.1.3/§8 and `11-service-composition.md`
  §5.1.4 (external agg/storage is explicitly Prometheus/VictoriaMetrics's
  job, not farc's).
- `hls_server` (`internal/hlsapi`) and `msm_server` (`internal/archivesapi`)
  expose **no** `/metrics` endpoint at all today.
- **Logs today**: plain unstructured text via stdlib `log.Logger.Printf`
  (`cmd/*/commands/default.go`), one `*log.Logger` per binary, written to
  `os.Stderr` plus optionally `<FARC_LOG_DIR|HLS_SERVER_LOG_DIR|
  MSM_SERVER_LOG_DIR>/<service>.log` if that env var is set (unset in both
  `docker-compose.yaml` and `deploy/docker-compose.release.yaml` today, so
  in practice: stderr only, captured by Docker's default `json-file` log
  driver). Every long-lived component logs through the same
  `func(format string, args ...any)` callback signature
  (`SetLogger`/`logf` convention, used uniformly across `farcd`/`hlsd`/
  `msmd`/`ingest`/etc.) — no per-line severity field, no structured
  key=value/JSON, just ad hoc inline text (e.g. some lines embed a literal
  `"WARNING:"` substring, most don't have any level marker at all).
- **`log/slog` was already considered and explicitly rejected** (PLAN.md
  Phase 24, 2026-08-xx): "every long-lived component already logs through
  the string-formatted `logf` callback, and switching just the new
  access-log line to structured output while leaving everything else as-is
  would be an inconsistent half-migration for no real gain." Phase 24 did
  add `X-Request-Id`/`X-Session-Id` access-log tracing
  (`internal/tracing`) and the optional `*_LOG_DIR` file logging, still
  plain text.
- **No existing Prometheus/Loki/Grafana/Promtail anywhere** in the repo:
  not in `docker-compose.yaml` (root, dev), `deploy/docker-compose.release.yaml`
  (release template), or `e2e/docker-compose.e2e.yaml`. `docs/docs/archive/
  01-architecture.md`/`02-storage.md`/`11-service-composition.md` all
  depict "Prometheus/VictoriaMetrics" as an external, out-of-repo consumer
  of `/metrics` — never as something this repo deploys itself.
- Existing opt-in-service convention in `docker-compose.yaml`: Compose
  `profiles:` (`s3` for `seaweedfs`, `msm` for `msm_server`) — a plain
  `docker compose up` never starts these; `--profile <name> up` does.
- No existing counters to expose cheaply for hls_server/msm_server:
  `internal/segmentcache` tracks no hit/miss stats; `internal/msmd`'s WS
  connection state to farcd is a local loop variable, not exported.

## Design decisions (grilling, 2026-08-19)

### Logging (structured level)

- Add a uniform `level=INFO`/`level=WARN`/`level=ERROR` prefix to every
  log line across all three binaries — both the ~40 internal `logf(...)`
  call sites (farcd/hlsd/msmd/ingest/tocindex/tracing) **and**
  `cmd/*/commands/default.go`'s direct `*log.Logger`/`log.Fatalf` calls
  (start/stop/fatal-config-load messages).
- Mechanism: a small new shared package (name TBD in planning, e.g.
  `internal/levellog`, mirroring `internal/tracing`'s precedent as a
  cross-binary concern) wrapping an existing `func(format string, args
  ...any)` with `.Info(format, args...)`/`.Warn(...)`/`.Error(...)`
  methods, each just doing `logf("level=X "+format, args...)`. The
  public `SetLogger(func(format string, args ...any))` signature is
  **unchanged** everywhere — tests passing `t.Logf` and `cmd/*` passing
  `logger.Printf` keep working untouched; the wrapper is constructed
  once inside each component from its own `logf` field.
- Level-assignment rule (apply uniformly, no per-line sign-off): **INFO**
  = routine lifecycle (start/stop, access log); **WARN** = recoverable
  anomaly (RTSP reconnect, keyframeGate timeout, non-fatal tick/decode
  errors, dropped frames); **ERROR** = an operation that actually failed
  (write verify failures, fatal config/startup errors before `os.Exit`,
  `farcd.Run` exiting with an error). Unusual/ambiguous lines get flagged
  for review in the final summary rather than guessed at silently.
- This is a real, intentional revisit of PLAN.md Phase 24's rejection of
  partial `slog` migration — that rejection was about a *structured*
  half-migration; this is a much smaller, uniform *level-prefix* addition
  applied consistently everywhere, not a partial one.

### Metrics

- Adopt `github.com/prometheus/client_golang` in all three binaries
  (first real dependency of this kind in `go.mod`). `promhttp.Handler()`
  gives free `go_*`/`process_*` runtime metrics (goroutines, memory,
  uptime, GC) to `hls_server` and `msm_server` with no domain code.
- `farcd`: migrate the existing hand-rolled `internal/api/metrics.go`
  (`writeUnitMetrics`) onto `client_golang` — same metric names/labels
  preserved (`farc_fblocks_total{storage=...}` etc.), implemented as a
  custom `prometheus.Collector` that reads live state at scrape time
  (matches the existing "no history, read on demand" design already
  documented in `02-storage.md` §4.1.3/§8).
- `hls_server`: new `/metrics` on `internal/hlsapi`'s `HttpApiServer`,
  runtime metrics plus one new domain gauge — count of currently
  connected/tracked channels (from `Hlsd`'s existing `tracked
  map[uint16]*trackedSub`).
- `msm_server`: new `/metrics` on `internal/archivesapi`'s server,
  runtime metrics plus one new domain gauge — WS connection status to
  farcd's event feed (0/1), requires exposing `internal/msmd`'s currently
  local connect/disconnect loop state.

### Deployment (docker-compose)

- Add `prometheus`, `loki`, `promtail`, `grafana` services to **both**
  `docker-compose.yaml` (dev) and `deploy/docker-compose.release.yaml`
  (release template) — always on by default, no new Compose `profile`
  (unlike `s3`/`msm`), since observability is the explicit point of this
  request, not an optional add-on.
- Log shipping: a `promtail` container reading Docker's own
  `json-file`-driver logs directly (mounts `/var/lib/docker/containers`
  + `/var/run/docker.sock` read-only, Docker service-discovery scrape
  config) — zero changes needed to how farc/hls_server/msm_server/web
  write their own stdout/stderr. Explicitly not the Loki Docker logging
  driver (would need a host-level plugin install outside docker-compose).
- Prometheus scrapes: `farc:9090/metrics`, plus new `hls_server`/
  `msm_server` metrics ports (chosen during planning, following the
  existing `FARC_METRICS_PORT`-style env var convention in
  `internal/hlsconfig`/`internal/msmconfig`).
- Grafana: published to a host port (dev default likely `3000`, like
  `web` is published on `80` today); default `admin`/`admin`-style login
  via `GF_SECURITY_ADMIN_PASSWORD` env var. Prometheus/Loki stay
  internal-network-only (no host port), reachable through Grafana.
- Dashboards/datasources are provisioned as code: JSON dashboard files +
  a `datasources.yaml` committed to the repo (new `deploy/observability/`
  or similar directory, exact layout decided during planning) and
  bind-mounted into the Grafana container's provisioning directories —
  seeded automatically on first start, matching the repo's existing
  `config.EnsureExists`-style "config as code" convention.
- Three dashboards in v1: **Storage & Fblocks** (farcd's fblock-state/
  write-queue/write-failure metrics), **Services Overview** (runtime
  health of all three binaries + the two new domain gauges), **Logs**
  (Loki: log volume by level/service, live tail panel).

### Process

- Plan Mode was proposed next, but the user explicitly said to go
  straight to TDD instead when confirming the design ("верно переходи").
  TDD covered the Go-code portions (levellog package, metrics
  Collector, new domain gauges) — infra/YAML/JSON provisioning has no
  natural red/green cycle and was verified by actually bringing the
  stack up instead (see Fix/Verification below).

## Fix

- `internal/levellog` (new package): `Logger.Info/.Warn/.Error` wrap an
  existing `func(format string, args ...any)` and prepend `level=X `.
  Wired into all ~40 `logf` call sites across `internal/{ingest,hlsd,
  msmd,farcd,tocindex,tracing}` and `cmd/*/commands/default.go`'s own
  start/stop/fatal-config lines, per the agreed INFO=lifecycle/
  WARN=recoverable-anomaly/ERROR=real-failure rule.
- `github.com/prometheus/client_golang@v1.19.1` added to `go.mod` (not
  `@latest` — see PLAN.md Phase 25 for why). `internal/api/metrics.go`
  migrated from hand-rolled Prometheus text exposition to a
  `prometheus.Collector` (`storageCollector`), same metric names/
  labels. `internal/hlsd` and `internal/msmd` each gained a `/metrics`
  (new `HLS_SERVER_METRICS_IP/PORT`, `MSM_SERVER_METRICS_IP/PORT`
  config, both required) serving `client_golang`'s free `go_*`/
  `process_*` collectors plus one domain gauge each:
  `hls_server_connected_channels` (`Hlsd.connectedChannels
  atomic.Int32`, refreshed in `persist`, the one place `reconcile`'s
  single-goroutine-owned `tracked` map is ever mutated) and
  `msm_server_ws_connected` (`atomic.Bool`, flipped in `run`'s
  subscribe/disconnect loop).
- `docker-compose.yaml` / `deploy/docker-compose.release.yaml`: new
  `prometheus`/`loki`/`promtail`/`grafana` services, always on.
  `deploy/observability/`: `prometheus.yml` (3 static scrape targets),
  `promtail-config.yaml` (`docker_sd_configs` against `docker.sock`,
  no code/log-driver change needed), `grafana/provisioning/` (Prometheus
  + Loki datasources) and `grafana/dashboards/` (Storage & Fblocks,
  Services Overview, Logs — 3 JSON files, auto-provisioned).

## Tests

TDD red→green for the Go portions:
- `internal/levellog/levellog_test.go`: `.Info`/`.Warn`/`.Error` each
  prepend the right `level=` token; nil-logf no-op.
- `internal/api/metrics_test.go`: `TestHandleMetrics_NoStorages` updated
  (red on the old "body is fully empty" assertion, since runtime
  collectors are now always present) to assert no `farc_fblocks_total`
  leaks with zero storages, plus `go_goroutines` is present.
- `internal/hlsd/hlsd_metrics_test.go` (new):
  `TestRun_MetricsEndpoint_ReportsConnectedChannels`/`_NoChannels` —
  real `Hlsd.Run` against a fake farcd, polling `/metrics` until
  `hls_server_connected_channels` reflects the real tracked-channel
  count.
- `internal/msmd/metrics_test.go` (new):
  `TestRun_MetricsEndpoint_ReportsWSConnectionStatus` — real `Run`
  against a real in-process farcd, polling `/metrics` until
  `msm_server_ws_connected 1` appears after the WS subscription
  actually connects.
- `internal/hlsconfig`/`internal/msmconfig` test suites extended for
  the new required `*_METRICS_PORT` env var (red on the six/two tests
  that didn't set it, then fixed).
- Full-repo verification, every cycle: `go build ./...`, `go test
  ./...` (all packages), `go test ./internal/{api,hlsd,msmd}/... -race`,
  `golangci-lint run ./...`, `gofmt -l` — all clean (only pre-existing,
  untouched-by-this-work findings remain: 2 `noinlineerr` hits in
  `internal/api/eventpush.go`, 5 in `internal/storage/segment.go`; one
  pre-existing import-order `gofmt` diff in `internal/hlsd/hlsd.go`/
  `internal/tocindex/subscriber_test.go`).

## Verification (infra, no red/green cycle)

- `docker compose config` valid for both compose files; every new
  YAML/JSON file (`prometheus.yml`, `promtail-config.yaml`,
  `datasources.yaml`, `dashboards.yaml`, 3 dashboard JSONs) parses
  cleanly.
- Brought up the real `prometheus`/`loki`/`promtail`/`grafana` half of
  `docker-compose.yaml` (Docker Hub images pull fine in this sandbox;
  `farc`/`hls_server`/`msm_server` could not be built here — see
  PLAN.md Phase 25's mirror-gap note) and confirmed live, via
  `docker exec` (the sandbox's own host-level proxy blocks host→
  container curl, unrelated to the stack itself):
  - Prometheus's 3 scrape targets (`farc:9090`, `hls_server:9091`,
    `msm_server:9092`) are configured exactly as intended (each `down`
    only because those containers don't exist in this run).
  - Grafana's `/api/datasources` shows both Prometheus and Loki with
    the intended `uid`s/URLs; `/api/search` shows all 3 dashboards
    auto-provisioned into the `farc` folder with the right panel counts.
  - Promtail actually shipped `farc-grafana-1`/`farc-loki-1`/
    `farc-prometheus-1`/`farc-promtail-1`'s own container logs into
    Loki, queryable live via `/loki/api/v1/query`; the Logs dashboard's
    `regexp`-based level-extraction LogQL query executes successfully
    against real data (returns 0 for `level=` matches only because none
    of the currently-running containers emit that format — expected,
    since only `farc`/`hls_server`/`msm_server` do).
  - Stack torn down (`docker compose down -v`) after verification.
