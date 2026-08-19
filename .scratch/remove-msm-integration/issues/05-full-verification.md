# 05 — Full verification

Status: open
Blocked by: 01, 02, 03, 04

## Task

This is a deletion-only effort with no behavior change to farcd/
hls_server's actual RTSP/HLS code paths (confirmed during grilling: the
dependency edge only ran from the msm cluster into core farc packages,
never the other way), so unlike `dependency-upgrade`'s issue 07, a full
Docker/Playwright e2e run is not needed here — the point of that depth
last time was to catch decode/player regressions from a dependency
major-bump, which doesn't apply to a pure deletion. Still verify
thoroughly that nothing was left dangling:

- `go build ./...`, `go vet ./...`, `go test ./... -race`,
  `golangci-lint run` — all green, no new findings beyond whatever
  pre-existing ones already exist unrelated to this work (check with
  `git stash` against current `main` if anything unexpected shows up).
- `go test -tags e2e ./tests/...` — confirmed during grilling this file
  has no msm/archivesapi references at all; should be unaffected, run it
  anyway as a real-process sanity check that farcd/hls_server still
  start up and serve correctly with the msm packages gone.
- `task build` (all binaries + web) succeeds with exactly two Go
  binaries now (`farc`, `hls_server`) — confirm `msm_server` is no
  longer produced anywhere.
- Repo-wide final sweep: `/usr/bin/grep -RIl -E
  "msm|archivesapi|farcctl|vaa[-_]?block" .` (using the real grep binary,
  not this repo's `ugrep`-aliased one — see issue 01) returns nothing
  except this `.scratch/remove-msm-integration/` effort's own files and
  any other `.scratch/**` issue files explicitly left alone per issue
  04's scope (incidental mentions in unrelated efforts).
- `docker compose -f docker-compose.yaml config` and `docker compose -f
  e2e/docker-compose.e2e.yaml config` both parse cleanly (the e2e
  compose file never referenced msm_server, so this just confirms issue
  02 didn't break the main compose file).

## Comments
