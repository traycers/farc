# Promote channel-node/time-range walking into the `toc` package

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — grilling found the duplication was wider than this ticket's original draft: also `findChildByRole` (4 sites) and `channelSubtree` (2 explicit + 2 inlined)

## Problem

Five packages each independently reimplement "given a channel number, find
its node in the TOC tree, then reduce its subtree's time range" purely in
terms of `toc.ScanByRole`/`toc.SubtreeRange`/`toc.InlineValue`/`toc.InRange`:

- `internal/api/query.go` — `findChannelNode`, `findChildByRole`,
  `channelFrameTimesInRange`
- `internal/tocindex/subscriber.go` — its own `findChannelNode` and
  `channelTimeRange` (re-deriving the same min/max-over-`SubtreeRange`
  reduction `query.go`'s `channelFrameTimesInRange` already does)
- `internal/segment/helpers.go` — own `findChannelNode`
- `internal/playlist/helpers.go` — own `findChannelNode`
- `internal/vaablocks/vaablocks.go` — own `findChannelNode`, whose doc
  comment explicitly says it's "duplicated from internal/api/query.go's own
  unexported helper of the same name/shape (not importable across
  packages, and small enough not to warrant promoting to a shared package
  of its own)" — that tradeoff no longer holds now that a fifth consumer
  exists.

A fix to channel-node resolution (e.g. handling a future non-uint32
encoding) currently means touching five packages, not one.

## Design (settled via grilling, 2026-08-19)

Grilling surfaced a wider duplication footprint than the original draft
and a scope decision: promote only the pieces that are **byte-identical**
across sites (zero divergence risk), and leave the two genuinely different
time-range reducers (`channelFrameTimesInRange`'s bounded `[t1,t2]` list of
node ids vs. `channelTimeRange`'s unbounded min/max scalar pair) where they
are, built on top of the new shared primitive instead of force-unified.

Added to `toc/query.go`:
- `toc.ChannelNode(c *Columns, channel uint16) (uint32, bool)`
- `toc.ChildByRole(c *Columns, parentID uint32, role mediatree.Role) (uint32, bool)`
- `toc.ChannelSubtreeRange(c *Columns, channel uint16) (start, end uint32, ok bool)`
  (= `ChannelNode` + `SubtreeRange`, the actual shared starting point every
  caller needs)

## Fix (2026-08-19)

Implemented via TDD (3 new tests in `toc/query_test.go`, against the
existing `sampleTree()`/doc-table fixture already used by
`TestSubtreeRangeMatchesDocTable` — no new fixture needed), then all 5
sites rewired and their local duplicates deleted:

- `internal/api/query.go`: `findChannelNode`/`findChildByRole` deleted,
  `channelFrameTimesInRange` now calls `toc.ChannelSubtreeRange` directly;
  `encoding/binary` import removed (no longer used in this file).
- `internal/tocindex/subscriber.go`: local `findChannelNode` deleted,
  `channelTimeRange` now calls `toc.ChannelSubtreeRange`; `encoding/binary`
  import removed. `internal/tocindex/videopresence.go`'s
  `VideoPresenceSegments` also rewired.
- `internal/segment/helpers.go`: **deleted entirely** (contained only these
  three now-promoted functions). `init.go`/`media.go` call sites rewired to
  `toc.ChannelSubtreeRange`/`toc.ChildByRole`.
- `internal/playlist/helpers.go`: **deleted entirely**, same reason.
  `config.go`/`segments.go` rewired.
- `internal/vaablocks/vaablocks.go`: local `findChannelNode`/
  `findChildByRole` deleted (its own doc comment explaining why it
  "wasn't worth promoting" removed along with it — that tradeoff no
  longer applied once a fifth consumer existed). `streamconfig.go`'s 5
  call sites and `vaablocks.go`'s own remaining 2 rewired.
- No behavior change: full `go test ./...` green, `go test -race` on all 6
  touched packages green, `golangci-lint run` on each of the 6 shows 0
  issues (the only lint hits found during verification, in
  `internal/api/eventpush.go`, are pre-existing and in a file this ticket
  never touched). `gofmt -l` clean.

## Comments
