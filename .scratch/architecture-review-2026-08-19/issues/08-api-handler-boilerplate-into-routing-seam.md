# Fold farcd's HTTP handler boilerplate into the routing seam

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — hlsapi's handleInit/handleMedia bonus deliberately deferred, see Comments

## Problem

Three small pieces of boilerplate are each copy-pasted across
`internal/api` handlers:

- `if s.ing == nil { writeError(w, http.StatusNotImplemented, errNoIngestManager); return }`
  — identical 4-line guard repeated in `internal/api/channels.go` (7
  handlers) and `internal/api/fblocktree.go` (~line 263).
- "resolve storage-by-id or 404" — `unit, ok := s.reg.Get(id); if !ok {
  writeError(w, http.StatusNotFound, fmt.Errorf("api: unknown storage %q",
  id)) }` — repeated 6+ times across `storages.go`, `catalog.go`,
  `fblocktree.go`, `query.go`, even though `internal/api/fcontainers.go`'s
  `resolveUnitAndUUID` already proves the package knows this pattern for
  the fcontainer+uuid case; it was just never extended to the plain
  storage-id case.
- `errors.Is(err, ingest.ErrWrongPolicyType) → 409` — identical 4-line
  block in `channels.go`'s `handleTriggerEvent`, `handleStartRecording`,
  `handleStopRecording`.

One level up, `internal/hlsapi/handlers.go`'s `handleInit`/`handleMedia`
duplicate the same cache-lookup/build/store control flow around different
build calls — worth folding in if convenient, but is its own smaller
sub-task.

## Design (settled via grilling, 2026-08-19)

Investigation found one exception that must NOT get the `requireIngest`
treatment: `handleListChannels` (`GET /channels`) has its own `s.ing == nil`
check, but it degrades to `200 {[]}` (an empty channel list), not `501` —
a deliberately different behavior from the other 8 sites, easy to miss on
a pure grep-and-replace. Confirmed by reading its body, not just counting
`s.ing == nil` occurrences.

Otherwise the original proposal held: `requireIngest` middleware applied
once in `routes()`, a `resolveUnit(w, r) (*storage.Unit, string, bool)`
helper next to `resolveUnitAndUUID`, and a `writeCommandError(w, err)`
helper folding the `ErrWrongPolicyType`→409 mapping (simpler than
routing it through the existing `apiError`/`writeAPIError` machinery,
since this mapping doesn't need a caller-supplied default status the way
that machinery exists for).

## Fix (2026-08-19)

Implemented via TDD:

- `internal/api/server.go`: new `requireIngest(h http.HandlerFunc) http.HandlerFunc`
  method, wrapping 8 of the 9 `s.ing`-dependent routes in `routes()` —
  `handleListChannels` deliberately left unwrapped, with a comment
  explaining why.
- `internal/api/channels.go`/`fblocktree.go`: the 8 individual
  `if s.ing == nil {...}` guard blocks deleted from handler bodies (the
  9th, inside `handleListChannels`, is intentionally different behavior
  and stays).
- `internal/api/fcontainers.go`: new `resolveUnit` helper; the existing
  `resolveUnitAndUUID` rewritten to call it instead of duplicating the
  storage-lookup half itself (a small bonus dedup beyond the ticket's own
  scope).
- `internal/api/catalog.go`, `fblocktree.go` (both sites),
  `query.go` (both sites), `storages.go`: all 6 plain storage-id lookups
  rewired to `resolveUnit`, with matching unused-import cleanup
  (`encoding/hex`'s `fmt`/`github.com/gorilla/mux` in files that no
  longer need them directly).
- `internal/api/helpers.go`: new `writeCommandError`; `channels.go`'s
  `handleTriggerEvent`/`handleStartRecording`/`handleStopRecording` all
  call it instead of repeating the status-mapping block.
- New `internal/api/helpers_test.go`: `TestRequireIngest_NoIngestManager_Returns501WithoutCallingHandler`,
  `TestRequireIngest_WithIngestManager_CallsThrough`,
  `TestWriteCommandError_WrongPolicyTypeIs409`,
  `TestWriteCommandError_OtherErrorIs404` — direct unit tests of the two
  new helpers, no HTTP routing needed. `resolveUnit`'s not-found path
  wasn't given a bespoke test — it's already exercised end-to-end by
  existing per-handler "unknown storage" tests across `catalog_test.go`,
  `fblocktree_test.go`, `channels_test.go`, `fcontainers_test.go`, and
  `eventpush_test.go`, which all kept passing unchanged.
- Full `go test ./...` green, `go test -race ./internal/api/...` green,
  `golangci-lint run ./internal/api/...` shows only the pre-existing,
  unrelated `internal/api/eventpush.go` debt. `gofmt -l` clean.

## Comments

Deferred: `internal/hlsapi/handlers.go`'s `handleInit`/`handleMedia`
cache-lookup/build/store duplication, flagged in the original problem
statement as "its own smaller sub-task." Not touched in this pass — it's a
different package with its own seam, not part of the `internal/api`
routing-seam scope this ticket actually closed. Worth its own ticket if
wanted.
