# 02 — Clean up build/taskfile/Docker/observability references

Status: open
Blocked by: 01

## Task

- `taskfile.yaml`: remove `vars.z_msm_name`/`z_msm_file_name`, the
  `build/msm_server` task, and its entry in `build`'s `deps` list.
- `docker-compose.yaml`: remove the `msm_server` service (currently
  gated behind the opt-in `msm` compose profile). Check whether the
  `msm` profile is referenced anywhere else in that file (e.g. other
  services conditionally depending on it) before removing the profile
  concept itself — if the profile becomes unused everywhere, drop it,
  not just this one service.
- `deploy/docker-compose.release.yaml`: remove the one passing comment
  mentioning msm_server (no actual service is defined there — this is a
  comment-only cleanup).
- `deploy/observability/prometheus.yml`: remove the `job_name:
  msm_server` scrape target.
- `deploy/observability/promtail-config.yaml`: remove the comment
  mentioning msm_server.
- `deploy/observability/grafana/dashboards/logs.json`: remove
  msm_server from job-label regexes.
- `deploy/observability/grafana/dashboards/services-overview.json`:
  remove msm_server from job-label regexes AND remove the dedicated
  "msm_server WS connected to farcd" panel (`msm_server_ws_connected`
  metric) — this is a whole panel, not just a label tweak, so check the
  dashboard still renders sensibly (grid layout / panel IDs) after
  removing it.

## Explicitly NOT needed

- No `.github/workflows/ci.yml` changes — confirmed during grilling
  there's no dedicated msm_server step; it was only implicitly
  built/tested via the top-level `go build ./...`/`go test ./...
  -race`, which simply stops touching it once issue 01 lands.
- No `.github/workflows/release.yml` changes — it never referenced
  `Dockerfile.msm_server` or built an msm_server image; already
  `farc`/`hls_server`/`web` only.
- No `.dockerignore` change — it never had an msm_server-specific entry.

## Verify

- `task build` succeeds (confirms the taskfile no longer references a
  deleted `cmd/msm_server`).
- `docker compose -f docker-compose.yaml config` (with and without
  `--profile msm` if that flag usage still makes sense post-edit)
  parses cleanly with no dangling references.
- Spot-check the two Grafana dashboard JSON files still parse as valid
  JSON after editing (`jq . <file> >/dev/null`).

## Comments
