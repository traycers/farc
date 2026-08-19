# 01 — Delete the msm_server binary and its packages

Status: open

## Task

Delete, in full:

- `cmd/msm_server/` (`main.go`, `commands/default.go`, `commands/index.go`)
- `internal/msmd/` (`msmd.go`, `params.go`, `metrics.go`, plus its
  `*_test.go` files)
- `internal/msmclient/` (`msmclient.go` + test)
- `internal/msmapi/` (`msmapi.go` + test)
- `internal/msmconfig/` (`config.go` + test — msm_server's env-only
  process config; imported by `msmd.go` and
  `cmd/msm_server/commands/default.go`, nowhere else)
- `internal/archivesapi/` (`server.go`, `helpers.go`, plus its
  `*_test.go` files)
- `internal/farcctl/` (`farcctl.go` + its `*_test.go` files)
- `internal/vaablocks/` (`vaablocks.go`, `streamconfig.go` + test)
- `Dockerfile.msm_server`

Then `go mod tidy`.

## Verify (before touching anything else)

Confirmed during grilling (2026-08-19, full-repo scan) that:

- Nothing outside these 8 dirs imports any of them — no ripple into
  `internal/farcd`, `internal/api`, `internal/hlsd`, `internal/hlsapi`,
  `cmd/farc`, `cmd/hls_server`, `internal/segment`, `internal/tocindex`,
  etc. `go build ./...`/`go vet ./...` should go straight to green after
  deletion with zero code changes elsewhere in this issue.
- Third-party deps the msm cluster used (`spf13/cobra`,
  `joho/godotenv`, `prometheus/client_golang/*`) are all also used by
  `cmd/farc`/`cmd/hls_server`/`internal/api`/`internal/hlsd` — `go mod
  tidy` should NOT drop them from `go.mod`; if it does, something
  outside the assumed scope depended on the msm cluster specifically for
  that dep and needs investigating before proceeding.
- `internal/hlsclient` must NOT be touched — it's hls_server's own
  client, msmd only reused it. If deleting the msm cluster causes any
  compile error in `internal/hlsclient` itself, that means it had
  msm-specific code mixed in that wasn't caught during grilling — stop
  and re-scope rather than deleting into a shared package.

Re-grep after deleting to confirm nothing was missed: `/usr/bin/grep
-RIl -E "msm|archivesapi|farcctl|vaa[-_]?block" --include='*.go' .`
should return zero hits. (Use `/usr/bin/grep` directly, not this repo's
aliased `grep` — the alias silently treats files containing null bytes,
e.g. `CLAUDE.md`, as binary and skips them under `-r`.)

## Also touch while here

`internal/api/storages.go`'s `handleRemoveStorage` doc comment currently
says "the only expected caller (archivesapi)" and its surrounding
comments explain the 409-refusal behavior in terms of archivesapi always
removing channels first. Reword so it no longer refers to a caller that
no longer exists in this repo — describe the 409 behavior on its own
terms (any caller must remove every attached channel first) rather than
naming archivesapi specifically. Don't otherwise change its behavior;
see the separate `storage-detach-button` issue for its next real caller.

Similarly check `internal/storage/writetxn.go`'s `EventFblockDeleted`
comment and `internal/farcd/farcd.go`'s `JournalEvent.Storage` comment —
both cite msm_server as *a* reason they exist, not the *only* reason
(they're also consumed by `internal/tocindex`, `internal/segmentcache`,
and the web UI). Reword to drop the msm_server mention, keep the rest.

## Verify

`go build ./...`, `go vet ./...`, `go test ./... -race`, `golangci-lint
run`, `go mod tidy` (confirm no unexpected deps drop out of
`go.mod`/`go.sum`).

## Comments
