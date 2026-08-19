# 04 — remove gorilla/mux, revert to stdlib net/http.ServeMux

Status: resolved
Blocked by: 01

## Why this is safe as a mechanical port

Confirmed during grilling (2026-08-19) via
`grep -rn "mux\.\(Vars\|NewRouter\|Router\)"` and a scan for
`NotFoundHandler`/`MethodNotAllowedHandler`/`.Use(`/`StrictSlash`/
`PathPrefix` across the three affected packages: none of gorilla/mux's
extra features (regex-constrained `{id:[0-9]+}` patterns, custom
404/405 handlers, router-level middleware, sub-routers) are used
anywhere. Every route is a plain `{name}` wildcard + `.Methods(...)`,
which maps directly onto go1.22+'s enhanced `http.ServeMux`:

```go
// before
r.HandleFunc("/storages/{id}", h).Methods(http.MethodPatch)
id := mux.Vars(req)["id"]

// after
mux.HandleFunc("PATCH /storages/{id}", h)
id := req.PathValue("id")
```

Routes registering multiple methods on the same path (e.g. `/storages`
GET+POST, `/channels` GET+POST) become two separate pattern strings —
stdlib `ServeMux` handles this natively, no behavior change.

## Files to touch

- `internal/api/server.go`, `storages.go`, `channels.go`,
  `fcontainers.go`, `fblocktree.go` — note `server.go` constructs a
  `mux.Router` in more than one place (struct-literal init AND a
  separate `mux.NewRouter()` call further down at time of writing —
  re-grep, don't trust line numbers here) — get both.
- `internal/hlsapi/server.go`, `handlers.go`
- `internal/archivesapi/server.go`

Search for every `mux.Vars(`, `mux.NewRouter(`, `.Methods(` call across
these three packages and convert each.

## Watch for

- `s.mux.Handle("/metrics", s.metricsSrv).Methods(http.MethodGet)` —
  `Handle` (not `HandleFunc`) taking an `http.Handler` — stdlib
  `ServeMux.Handle` takes the same `"METHOD /path"` pattern string, so
  this converts the same way, just check the type still fits.
- `s.requireIngest(...)` wrapping — these return `http.HandlerFunc`
  already, unaffected by the router swap.
- Do NOT touch `gorilla/websocket` usage in `eventpush.go`,
  `msmclient/msmclient.go`, `hlsclient/events.go`,
  `internal/tracing/tracing.go` — different package, staying.
  `tracing.go` has a doc comment mentioning "a gorilla/mux Router" for
  context (not an import) — reword it once mux is gone from the repo so
  it doesn't reference a package that no longer exists in `go.mod`.
- `go.mod`: remove the `github.com/gorilla/mux` require entirely once no
  import references it (`go mod tidy`).

## Tests

No test file directly calls `mux.Vars`/`mux.SetURLVars` (checked during
grilling) — tests exercise the router as a black box via
`httptest`/real HTTP requests, so they should need zero changes if the
route behavior is preserved. Run the full test suite for all three
touched packages (`internal/api`, `internal/hlsapi`, `internal/archivesapi`)
including any 404/405 assertions — stdlib `ServeMux`'s automatic
405-on-method-mismatch should match gorilla/mux's default behavior, but
verify explicitly rather than assuming.

## Verify

`go build ./...`, `go vet ./...`,
`go test ./internal/api/... ./internal/hlsapi/... ./internal/archivesapi/... -race`,
`go mod tidy` (confirm `gorilla/mux` drops out of `go.sum`).

## Comments

Landed 2026-08-19. Mechanical port confirmed correct in practice, not
just in theory: `s.mux.HandleFunc("/path", h).Methods(m)` →
`s.mux.HandleFunc("METHOD /path", h)` (stdlib `http.ServeMux`, go1.22+),
`mux.Vars(r)["x"]` → `r.PathValue("x")` (bulk `sed` across all four
`internal/api` files, `internal/hlsapi/{server,handlers}.go`,
`internal/archivesapi/server.go`), `mux.NewRouter()` →
`http.NewServeMux()`, `mux *mux.Router` field → `mux *http.ServeMux`.
`internal/archivesapi/server.go` additionally had two routes
(`PUT`/`DELETE /api/v1/archives/`, no `{aid}`) registering different
methods on the identical path — stdlib `ServeMux` handles that the same
way gorilla/mux did, no special-casing needed. Reworded
`internal/tracing/tracing.go`'s doc comment, which referenced "a
gorilla/mux Router" only in prose (no import) — now says
`*http.ServeMux`.

`go mod tidy` confirms `gorilla/mux` is fully gone from `go.mod`/`go.sum`
(`go mod why` reports "main module does not need" it). `gorilla/websocket`
untouched, as planned — separate package, still in `go.mod`.

Full test suite for the three touched packages green, including exactly
the 404/405-path tests this ticket flagged to verify explicitly (e.g.
`TestHandleRemoveStorage_UnknownID`, `TestHandleArchiveDetach_UnknownArchiveIs404`)
— stdlib `ServeMux`'s automatic 404/405 behavior matches gorilla/mux's
here. Full `go test ./... -race` and `golangci-lint run` on the touched
packages both green (only the two pre-existing, unrelated
`eventpush.go` `noinlineerr` findings from issue 01 remain — nothing new
introduced by this change).

**Follow-up caught during a post-hoc advisor review**: the "mechanical
port" claim above was true for `internal/api`/`internal/hlsapi` but
missed one real divergence in `internal/archivesapi` — every one of its
routes ends in a literal `/`. gorilla/mux always treated that as an
exact path; stdlib `net/http.ServeMux` treats a trailing `/` as a
**subtree prefix** by default, so e.g. `PUT /api/v1/archives/garbage`
would silently reach `handleArchiveSetup` (as if it were `PUT
/api/v1/archives/`) instead of 404ing at the router — confirmed
empirically with a throwaway `http.ServeMux` repro before touching real
code. Fixed by adding `{$}` to every pattern ending in `/` in
`internal/archivesapi/server.go`'s `routes()`. Verified the fix matters
with a genuine red→green cycle: wrote
`TestRoutes_RejectExtraPathSegments` (against a *real, already-created*
archive — not an unknown one, since several of these handlers 404 on an
unknown archive anyway via `requireArchive`, which would mask exactly
this router-level bug), confirmed it fails (400, not 404) with the `{$}`
fix reverted, then confirmed it passes with the fix restored.
`internal/api`/`internal/hlsapi` don't have this exposure — checked, no
route in either ends in `/`.

Also noted, not fixed (deliberate, real behavior delta not a bug):
stdlib's `"GET /path"` pattern also answers `HEAD` requests, where
`gorilla/mux`'s `.Methods(http.MethodGet)` used to 405 them.
