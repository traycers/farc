# 02 — gortsplib v4 → v5

Status: resolved
Blocked by: 01

## Task

`go get github.com/bluenviron/gortsplib/v5@v5.6.4` (check for a newer
patch first), remove the `v4` require, update all imports
`github.com/bluenviron/gortsplib/v4` → `.../v5`.

## Known API diff (from v4.13.1 docs/changelog — re-verify against
whatever v5 patch actually gets pulled)

- `internal/ingest/rtsp.go`'s `NewClient`: v4's
  `client.Start(scheme, host string) error` took the scheme/host
  explicitly (that's the reason v4 was picked over v5 originally in
  Phase 23 — v5 dropped this signature). Check v5's actual `Start`
  signature and adjust the call site — likely back to a parameterless
  `Start()` closer to what v5 originally had pre-Phase-23.
- `internal/ingest/rtsp_integration_test.go`: v4 has no
  `Server.NetListener()`, so the test captures the bound `:0` address via
  `Server.Listen`'s hook instead. Check whether v5 restores
  `NetListener()` — if so, this workaround can be reverted to whatever
  the original pre-v4-downgrade test looked like (check git history
  around the Phase 23 commit for the pre-downgrade version if simplifying
  it back is easy; otherwise leave the current `Listen`-hook approach in
  place, it still works under v5 too).

## Verify

`go build ./...`, `go test ./internal/ingest/... -race`. This package
already has real RTSP integration tests — make sure they pass, they're
the actual regression signal for this bump (unit tests here talk to a
real (test) RTSP server, not mocks).

## Comments

Landed 2026-08-19. Confirmed via v5.6.4's actual source
(`client.go`): `Start()` now takes no args — `Scheme`/`Host` moved to
`Client` struct fields, set in `NewClient` before returning the client.
`rtspSource.Start`'s signature and `channelingest.go`'s call site updated
to match; three test fakes (`onPacketOnlySource`, `dualMediaSource`,
`scriptedSource`) had their `Start(scheme, host string)` stubs trimmed to
match the interface. All `gortsplib/v4` imports across `internal/ingest`
bulk-renamed to `v5` via `sed`.

`Server.NetListener()` is indeed back in v5 (confirmed in `server.go`) —
simplified `rtsp_integration_test.go` to use it directly instead of the
v4-only `Listen`-hook capture workaround, dropping the now-unused `net`
import too.

`go get gortsplib/v5@v5.6.4` transitively pulled `mediacommon/v2` to
v2.9.3 on its own (v5 depends on a newer mediacommon) — so issue 03's
version bump is already done as a side effect; only its code-side
`codecs.*` migration remains.

Real end-to-end signal: `TestChannelIngest_RealRTSPServerH264EndToEnd`
(a real in-process gortsplib v5 server + real `*gortsplib.Client`)
passes. Full `go test ./internal/ingest/... -race` green.
