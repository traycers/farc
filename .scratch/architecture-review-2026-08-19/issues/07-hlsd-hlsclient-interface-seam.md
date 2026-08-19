# Put an interface at the seam between `hlsd` and `hlsclient` — imitate what `msmd` already does

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — scope widened significantly during grilling, see Design below

## Problem

`internal/hlsd/hlsd.go:262-412`'s ADR-021 channel-reconciliation loop
(`reconcile`/`reconcileOnce`/`applyRemoteList`/`startChannel`/`stopChannel`)
is genuinely pure, well-isolated set-diff logic (start what's
remote-but-untracked, stop what's tracked-but-no-longer-remote). But
`Hlsd.client` is typed as the concrete `*hlsclient.Client`
(`internal/hlsclient/hlsclient.go:33-41`, real HTTP+WS struct, no interface
at that seam), so nothing can substitute a fake. Every reconciliation test
in `internal/hlsd/hlsd_test.go` (`TestRun_ChannelCreatedOnFarcd_ServedWithoutRestart`,
`TestRun_ChannelMovedToDifferentStorage_ReindexesFromNewStorage`, etc.)
must spin up a real `storage.Unit`, a real farcd `HttpApiServer`+
`EventPushServer`, and the full `hlsd.Run` HTTP/WS stack just to exercise a
map diff.

Contrast: `internal/msmd` already defines `outbound`/`contentFetcher`/
`subscriber` interfaces specifically so `msmd_test.go` can drive
`run`/`consume`/`handle` with fakes (`fakeSubscriber`, `fakeOutbound`).
`hlsd` has no equivalent.

## Design (settled via grilling, 2026-08-19)

Investigation found the original proposal's 2-method interface
(`ListChannels`/`Subscribe`) insufficient: `Hlsd.startChannel` hands
`h.client` *whole* to `tocindex.NewEventSubscriber` (which calls
`GetTOC`/`Catalog`/`Subscribe`), and `internal/hlsapi`'s handlers hand the
same field to `internal/segment.BuildInit`/`BuildMedia` (which call
`ReadRanges`). Since `Hlsd.client` is one field threaded through all three
consumers, a narrow 2-method interface would have left `startChannel`/
`stopChannel` just as untestable as before (still forced through the
concrete type via `tocindex.NewEventSubscriber`'s own parameter).

Grilling chose the wider fix: one interface covering the union actually
used anywhere in the codebase — `Subscribe`, `ListChannels`, `GetTOC`,
`Catalog`, `ReadRanges` (`Candidates`/`Resolve` excluded — nothing outside
`hlsclient` itself calls them) — defined once in `hlsclient` itself
(`hlsclient.API`), not duplicated per-consumer. Go's structural interface
satisfaction means a single field typed as this union can still be passed
anywhere a narrower interface is expected (e.g. `tocindex.EventSubscriber`
declaring its own 3-method need would also have worked, but one definition
was judged simpler than three near-identical ones for a single shared
field).

## Fix (2026-08-19)

Implemented via TDD:

- `internal/hlsclient/hlsclient.go`: new `API` interface (5 methods).
  `*Client` satisfies it structurally — no change to `Client` itself.
- Four consumers switched from the concrete `*hlsclient.Client` to
  `hlsclient.API`: `internal/hlsd.Hlsd.client`,
  `internal/tocindex.EventSubscriber.client` (+ `NewEventSubscriber`'s
  parameter), `internal/hlsapi.Server.client` (+ `New`'s parameter), and
  `internal/segment.BuildInit`/`BuildMedia`'s `client` parameter.
- New `internal/hlsd/reconcile_test.go` (package `hlsd`, not the existing
  `hlsd_test` external package, specifically so it can reach
  `applyRemoteList`/`startChannel`/`stopChannel` and the `Hlsd` struct's
  unexported fields directly): a `fakeHLSClient` implementing
  `hlsclient.API` (`Subscribe`/`ListChannels` fully controllable;
  `GetTOC`/`Catalog`/`ReadRanges` fail fast, since they're only touched by
  `tocindex.EventSubscriber`'s own background bootstrap goroutine, not by
  reconciliation logic itself), plus `newTestHlsd` building a minimal
  `*Hlsd` (real `tocindex.Index`, `toccache.Cache`, `segmentcache.Cache`,
  and `hlsapi.Server` — all cheap to construct against a tempdir — no HTTP/
  WS server anywhere). 6 new tests directly exercise
  `applyRemoteList`/`startChannel`/`stopChannel`, running in 0.007s total
  (vs. the existing `TestRun_*` tests' real farcd + HTTP/WS stack).
- Existing `hlsd_test.go`'s `TestRun_*` tests kept exactly as-is — they
  still pass, now serving as integration coverage alongside the new unit
  tests rather than the only way in.
- Full `go test ./...` green, `go test -race` on all 5 touched packages
  (`hlsd`, `hlsclient`, `tocindex`, `hlsapi`, `segment`) green,
  `golangci-lint run` on each shows 0 issues, `gofmt -l` clean.

## Comments
