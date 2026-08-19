# Give `IngestManager` a `ReplaceChannel`, instead of two hand-rolled remove+add+rollback transactions

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`)

## Problem

`IngestManager` exposes only `AddChannel`/`RemoveChannel`. "Replace a
running channel's config without losing recording continuity" is instead a
compensating transaction hand-written at the HTTP layer:

- `internal/api/channels.go`'s `handleUpdateChannel` (~lines 427-500):
  `RemoveChannel` → `AddChannel(newConfig)` → on persist failure, remove
  the new one and restore the old.
- `createChannel` (~lines 339-388) has its own separate rollback-on-persist-
  failure logic.
- Separately, the 15-line `ingest.ChannelConfig{...}` struct literal is
  duplicated verbatim between `createChannel` (~355-369) and
  `handleUpdateChannel` (~463-477) — only the channel-id source differs.

Each rewrite of this transaction has slightly different failure-handling
shape; a bug fix to the swap/rollback guarantee has to be found and fixed
in more than one place.

## Design (settled via grilling, 2026-08-19)

`IngestManager.ReplaceChannel(channel uint16, newCfg ChannelConfig) (oldCfg ChannelConfig, err error)`
does `RemoveChannel` → `AddChannel(newCfg)`, restoring `old` itself if
`AddChannel` fails (rather than making the HTTP layer do that part). It
always returns the pre-swap config, even on failure, so a caller with its
own further step (persisting to disk) can roll back to it too — that
step is HTTP-layer business `ReplaceChannel` deliberately doesn't know
about. `removeChannel` (a different operation, not a swap) is untouched.

## Fix (2026-08-19)

Implemented via TDD:

- `internal/ingest/ingestmanager.go`: new `ReplaceChannel`.
- New tests in `internal/ingest/ingestmanager_test.go`:
  `TestIngestManager_ReplaceChannel_SwapsConfigAndReturnsOld` (happy path)
  and `TestIngestManager_ReplaceChannel_RestoresOldOnAddFailure` — the
  latter forces the internal `AddChannel(newCfg)` step to fail
  deterministically (`newCfg` claims a channel id already running
  elsewhere) rather than relying on a real concurrent race, to directly
  test the rollback contract.
- `internal/api/channels.go`: new `buildChannelConfig(channel, rtspURL,
  storageID, unit, policyType, capturePolicyRequest, name)
  ingest.ChannelConfig` used by both `createChannel` and
  `handleUpdateChannel`, replacing the duplicated 15-line struct literal.
  `handleUpdateChannel` now calls `s.ing.StorageOf(channel)` as an
  existence check (preserving the 404 response, with the same
  check-then-act race window this function's own doc comment already
  accepts elsewhere) before a single `ReplaceChannel` call; the
  persist-failure rollback branch is now one `ReplaceChannel(channel,
  old)` call instead of a manual `RemoveChannel`+`AddChannel(old)` pair.
- Full `go test ./...` green (including
  `TestHandleUpdateChannel_UnknownChannelIs404` and
  `TestHandleUpdateChannel_ReplacesFieldsAndPersists`, confirming
  unchanged HTTP status-code behavior), `go test -race` on
  `internal/ingest`/`internal/api` green, `golangci-lint run` on both
  shows only the pre-existing, unrelated `internal/api/eventpush.go`
  debt. `gofmt -l` clean.

## Comments
