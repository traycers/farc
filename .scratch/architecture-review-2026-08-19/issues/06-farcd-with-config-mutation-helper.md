# One `withConfigMutation` helper instead of six copies of lock/mutate/save/rollback

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

`internal/farcd/farcd.go:291-498`: `persistNewStorage`,
`persistUpdatedStorage`, `persistRemovedStorage`, `persistNewChannel`,
`persistUpdatedChannel`, `persistRemovedChannel` each repeat the identical
shape: `cfgMu.Lock()` → find index in a slice → save old value → mutate
`f.cfg` → `config.Save(f.configPath, f.cfg)` → on error, manually
reconstruct and restore the old slice via a hand-spliced rollback (repeated
near-verbatim in `persistRemovedStorage`/`persistRemovedChannel`). ~200
lines total, almost all of it transactional bookkeeping rather than the
handful of lines of real domain logic (what to persist, which events to
publish afterward).

## Design (settled via grilling, 2026-08-19)

Grilling surfaced a subtlety the original proposal's plain "mutate +
rollback" shape would have broken: two of the six methods have an
**idempotent no-op branch** (`persistNewStorage`/`persistNewChannel`,
"already present → return nil") that must skip `config.Save` entirely, not
just roll back after calling it — and must NOT run the caller's
post-success side effect (`bridgeFblockEvents`/publishing
`EventChannelCreated`). Also, `persistRemovedStorage` errors on "not
found" while `persistRemovedChannel` silently returns `nil` for the same
situation — an existing, deliberate asymmetry, not a bug, that the shared
helper must not paper over.

Final signature:

```go
func (f *Farcd) withConfigMutation(errCtx string, mutate func() (rollback func(), err error)) (mutated bool, err error)
```

`mutate` runs under `f.cfgMu`, applies its change directly to `f.cfg`, and
returns one of three things: `(rollback, nil)` after a real mutation
(`config.Save` runs; `rollback` undoes it on failure), `(nil, nil)` for an
idempotent no-op (`config.Save` never called), or `(nil, err)` when a
precondition fails before any change (also never calls `Save`).
`mutated` is `true` only once `Save` actually ran and succeeded, so each
caller gates its own post-success side effect on it.

## Fix (2026-08-19)

Implemented via TDD:

- `internal/farcd/farcd.go`: new `withConfigMutation`; all six
  `persist*` methods rewritten on top of it, each keeping its own
  find-index/no-op/error logic inside the `mutate` closure and its own
  post-success side effect (`bridgeFblockEvents`, `stopBridge` +
  `f.units` removal, or `f.push.Publish(...)`) gated on `mutated`. The
  `persistRemovedStorage`-errors-vs-`persistRemovedChannel`-silent-nil
  asymmetry preserved exactly.
- Two new characterization tests in `internal/farcd/farcd_test.go`,
  confirmed to already pass against the *pre-refactor* code before the
  refactor started (proving they test existing behavior, not new
  behavior), then re-verified green after:
  `TestFarcd_PersistNewStorage_AlreadyPresentSkipsSave` (points
  `configPath` at a nonexistent directory, then calls `persistNewStorage`
  on an already-present id — if `Save` were ever attempted it would fail,
  so a `nil` result proves the idempotent branch skips it) and
  `TestFarcd_PersistNewStorage_RollsBackOnSaveFailure` (same broken
  `configPath`, a genuinely new storage id — asserts the error surfaces
  *and* `f.cfg.Storages` is back to its original length).
- Three `//nolint:nilnil` comments added where a closure legitimately
  returns `(nil, nil)` for `withConfigMutation`'s own documented "nothing
  to do" signal — golangci-lint's `nilnil` linter otherwise flags the
  pattern with no built-in way to distinguish it from a real bug.
- Full `go test ./...` green, `go test -race ./internal/farcd/...` green,
  `golangci-lint run ./internal/farcd/...` shows 0 issues. `gofmt -l`
  clean.

## Comments
