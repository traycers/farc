# Dependency & toolchain upgrade

Status: resolved (all 7 issues landed 2026-08-19 — see PLAN.md Phase 26
and each issue's own Comments section for what actually happened)

Reached via `/grilling` on 2026-08-19. Goal: bring Go toolchain, Go module
deps, and web npm deps to their latest versions, including major-version
jumps where breaking changes exist. Also reverts two Phase-23 decisions
that were only forced by staying on `go 1.21.0`.

## Decisions (settled during grilling)

1. **Go toolchain**: `go.mod`'s `go` directive → `1.26.0` (current stable
   is `1.26.6`). No `toolchain` directive added — this repo never pinned
   one, and CI's `setup-go`'s `go-version` is the actual reproducibility
   source of truth. CI `go-version` and all three `Dockerfile.*` builder
   images (`golang:1.26-bookworm`) bumped to match.
2. **`gortsplib`**: v4.13.1 → v5.6.4 (major bump). Requires go≥1.25, no
   longer a blocker once (1) lands. API changes `Client.Start()` (no
   `scheme, host` args in v5) — touches `internal/ingest/rtsp.go` and
   `internal/ingest/rtsp_integration_test.go`.
3. **`mediacommon/v2`**: v2.1.0 → v2.9.3. Also requires go≥1.25. Restores
   `codecs.H264`/`codecs.MPEG4Audio` (added in v2.6.0) in place of the
   `fmp4.CodecH264`/`fmp4.CodecMPEG4Audio` workaround Phase 23 was forced
   into — `internal/segment/{init,media}.go`.
4. **Remove `gorilla/mux` entirely** — revert to stdlib `net/http`'s
   go1.22+ enhanced `ServeMux` (`"METHOD /path/{id}"` patterns +
   `r.PathValue("id")`). This was the *other* thing Phase 23's go1.21 pin
   forced; no longer needed once (1) lands. Confirmed mechanical (no
   regex-constrained patterns, no `StrictSlash`/custom
   `NotFoundHandler`/`Router.Use()` anywhere) across:
   - `internal/api/{server,storages,channels,fcontainers,fblocktree}.go`
   - `internal/hlsapi/{server,handlers}.go`
   - `internal/archivesapi/server.go`
   `gorilla/websocket` is a separate, unrelated package and stays
   untouched (`eventpush.go`, `msmclient`, `hlsclient/events.go`,
   `tracing.go`).
5. **Other Go deps** → latest within their current major (no API concerns
   expected): `aws-sdk-go-v2` family, `prometheus/client_golang` (was
   pinned to v1.19.1 specifically to avoid forcing the go1.25 bump —
   that reason is now moot), `gorilla/websocket`, `joho/godotenv`,
   `pion/rtp`, `spf13/cobra`, `golang.org/x/sys`, etc.
6. **web npm deps** → latest, including majors: React 18→19,
   `react-router-dom` 6→7 (app only uses `<BrowserRouter>`/declarative
   `<Routes>`/`<Route>`, no data-router APIs — v7 keeps this mode, so
   this is a version bump, not an architecture migration; keep importing
   from `react-router-dom`, no need to switch to bare `react-router`),
   Vite 6→8, TypeScript 5.7→7. Everything else (`hls.js`, `bootstrap`,
   `@testing-library/*`, `jsdom`, `@vitejs/plugin-react`) → latest
   patch/minor. CI's `setup-node` (`node-version: "22"`) already
   satisfies Vite 8's `engines` (`^20.19.0 || >=22.12.0`) and TS7/router7 —
   no bump needed there.
7. **Docs**:
   - `CLAUDE.md` line 7: "Go 1.21" → new version. While there, also fix
     the stale module path (`gitlab.rigel.bolid.ru/...`) left over from
     Phase 22's move to `github.com/traycers/farc` — unrelated drift,
     opportunistic fix.
   - `PLAN.md`: delete Phase 23 entirely (its whole premise — the go1.21
     pin — is being reversed; the `playlist.go` loop-var bug fix and the
     `gorilla/mux` port it documents are being reverted too, so nothing
     in that entry stays true). Add a new phase once this work lands,
     describing this upgrade.
8. **Verification depth** (Docker + `e2e/media/sample.mp4` confirmed
   present in the working environment): full stack —
   `go build`/`go vet`/`golangci-lint run`/`go test ./... -race`,
   `go test -tags e2e ./tests/...`, web `tsc -b`/`vite build`/
   `vitest run`, and the full `task test/e2e` (Docker Compose +
   Playwright against real RTSP media) — specifically because
   `gortsplib`/`mediacommon` (decode path) and hls.js/React (player) are
   exactly the two places a major bump could silently break real
   playback without unit tests noticing.

## Not in scope / explicitly decided against

- No React Router v7 "framework mode" migration — stays declarative.
- No `toolchain` directive pin in `go.mod`.
- Docker/CI Node version — not bumped, already sufficient.

## Issues

See `issues/01`–`07`. Work roughly in numeric order — later issues assume
earlier ones landed (see each issue's `Blocked by:` line).
