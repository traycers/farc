# 01 — Go toolchain bump

Status: resolved

## Task

Bump `go.mod`'s `go` directive from `1.21.0` to `1.26.0` (check
https://go.dev/dl/ first — use whatever the current stable release is by
the time this lands, e.g. `1.26.6` today). No `toolchain` directive (see
spec.md decision 1).

Then bump every place that currently pins a Go version to match:

- `.github/workflows/ci.yml` — all `go-version: "1.25.3"` occurrences
- `.github/workflows/release.yml` — `go-version: "1.25.3"`
- `Dockerfile.farc`, `Dockerfile.hls_server`, `Dockerfile.msm_server` —
  `FROM golang:1.25-bookworm AS builder` → `golang:1.26-bookworm`

## Also bump (same-major, no API risk expected)

Run `go get -u` for everything *except* `gortsplib`/`mediacommon/v2`
(those are issues 02/03 — major bumps with real API changes, keep them
isolated). At minimum:

- `github.com/aws/aws-sdk-go-v2` and its whole `feature/`/`service/`/
  `internal/` family
- `github.com/prometheus/client_golang` (was pinned to v1.19.1 in Phase 25
  specifically to avoid forcing a go1.25 bump — check the current latest,
  was v1.24.1 as of the grilling session)
- `github.com/gorilla/websocket`, `github.com/joho/godotenv`,
  `github.com/pion/rtp` (+ transitive `pion/rtcp`, `pion/sdp`,
  `pion/randutil`), `github.com/spf13/cobra`, `golang.org/x/sys`,
  `golang.org/x/net`, `google.golang.org/protobuf`

Do NOT bump `github.com/gorilla/mux` — it's being removed entirely in
issue 04, not updated.

## Verify

`go build ./...`, `go vet ./...`, `go test ./...` (unit only — full
verification is issue 07). Expect no code changes needed for this issue
beyond `go.mod`/`go.sum` and the version-string edits above.

## Comments

Landed 2026-08-19: `go.mod` → `go 1.26.0`, CI `go-version` → `1.26.6`
(confirmed `golang:1.26-bookworm` exists on Docker Hub before switching
all three Dockerfiles). Bumped `aws-sdk-go-v2` family, `pion/rtp` (+
transitive rtcp/sdp/randutil), `prometheus/client_golang` v1.19.1→v1.24.1
(the go1.25-avoidance pin from Phase 25 is now moot), `x/sys`, `x/net`,
`google.golang.org/protobuf` via `go get -u` + `go mod tidy`.
`gorilla/websocket`/`joho/godotenv`/`spf13/cobra` were already at latest
stable (checked `go list -m -versions` — godotenv's newer entries are
`-pre` prereleases, correctly skipped). `go build`/`go vet`/
`golangci-lint run`/`go test ./... -race` all green.

Found 5 pre-existing `noinlineerr` findings (`internal/api/eventpush.go:294,312`,
`internal/storage/segment.go:222,251,354`) — confirmed via `git stash` they
predate this work entirely (already present at the `2e4f4b8`/`d5090f7`
baseline, unrelated to any dependency bump). Left alone as out of scope
for this issue; flagged to the user separately.
