# 02 — Backend: expose TOC as flat SoA rows (ready + in_progress)

Status: open

## Task

Add two new read-only endpoints that project TOC data into flat rows
(`id, type, role, parent_id, sibling_id, value_or_offset, size` — spec
decision 3) instead of the nested `TreeNode` tree `handleReadFblockTree`/
`handleFblockLiveTreeWS` already build. Issue 03's table page is the sole
consumer.

### Ready fblocks — `GET /storages/{id}/fcontainers/{uuid}/toc/rows`

1. Register in `internal/api/server.go` next to the existing `.../tree`
   route (`server.go:194`).
2. New handler `handleReadTOCRows`, alongside `handleReadFblockTree`
   (`internal/api/fblocktree.go:120-131`). Same `resolveUnitAndUUID` guard
   (`internal/api/fcontainers.go:13-24`) and `unit.ReadTOC(uuid)` call
   (`fblocktree.go:125`) as the existing handler, but **skip
   `buildColumnsTree`/`columnsNode` entirely** (`fblocktree.go:94-116`) —
   project the returned `*toc.Columns` directly:
   ```go
   for i := uint32(0); i < c.N; i++ {
       rows[i] = tocRow{
           ID:            i,
           Type:          c.Type[i].String(),
           Role:          c.Role[i].String(),
           ParentID:      c.Parent[i],
           SiblingID:     c.Sibling[i],
           ValueOrOffset: c.ValueOrOffset[i],
           Size:          c.Size[i],
       }
   }
   ```
   This is strictly less code than the existing tree handler — no
   `toc.Children`/`toc.SubtreeRange` walk needed (those exist only to
   reconstruct nesting, which rows don't have).
3. `tocRow` struct (new, `fblocktree.go` or a new `toctable.go` in
   `internal/api`) with JSON tags `id`, `type`, `role`, `parent_id`,
   `sibling_id`, `value_or_offset`, `size` — matching `TreeNode`'s
   `parent_id` convention (`fblocktree.go:34`), not `toc.Columns`'s Go
   field names (spec decision 3).

### In-progress fblocks — `GET /storages/{id}/fblocks/{index}/toc/rows/ws`

4. Register next to the existing `.../tree/ws` route (`server.go:199`),
   same `s.requireIngest(...)` wrapping.
5. New handler `handleFblockLiveTOCRowsWS`, mirroring
   `handleFblockLiveTreeWS` (`fblocktree.go:260-311`) — same
   `fblock.InProgress`-state validation (260-279), same WS upgrade (281),
   same `fblockLiveSig{generation, elemCount}` resend-only-on-change
   pattern (231-238, 293, 302), same `500 * time.Millisecond` ticker
   (238). Reuses `s.ing.LiveTreeForStorage(storageID)`
   (`internal/ingest/ingestmanager.go:288-296`) for the `[]mediatree.Element`
   snapshot, but projects rows instead of calling `buildLiveTree`
   (`fblocktree.go:197-216`):
   ```go
   for i, e := range elems {
       rows[i] = tocRow{
           ID:            uint32(i),
           Type:          e.Type.String(),
           Role:          e.Role.String(),
           ParentID:      e.Parent,
           SiblingID:     e.Sibling,
           ValueOrOffset: liveValueOrOffset(e),
           Size:          liveSize(e),
       }
   }
   ```
   No DFS reorder needed — `Element.Parent[i] <= i` is already guaranteed
   in creation order (per `toc/build.go:59`'s and `fblocktree.go:195`'s
   comments), unlike the ready path where the reorder is what produces the
   row ids in the first place (spec decision 5).
6. `liveValueOrOffset(e mediatree.Element) uint64` — per spec decisions 5
   and 3: for `e.Type.Variable()` (bytes/string), return `0` (no
   Content-offset exists pre-finalization —
   `fblocktree.go:176-180`'s comment confirms `Element.Value` is already
   fully decoded bytes with no offset indirection). For fixed-width types,
   pack `e.Value` the same way `toc/build.go:98-104`'s `packInline` does
   for the ready path, so a live row and its eventual `ready` row carry the
   bit-identical numeric value for the same logical field — export
   `packInline`/`unpackInline` from `toc/build.go` (currently unexported;
   confirm exact line numbers when implementing — the research pass that
   fed this issue only pinned `unpackInline` at `toc/build.go:121-131`)
   if reusing them directly is cleaner than duplicating the switch.
7. `liveSize(e mediatree.Element) uint64` — **must not** be
   `len(e.Value)` unconditionally, or fixed-width nodes report a nonzero
   `size` live and `0` once `ready` (`toc.Columns.Size` is 0 for
   fixed-width types per `toc/build.go:98-104`'s `c.Size[newID] = 0`
   branch), breaking the "same shape regardless of state" promise (spec
   decision 5). Gate it the same way as `liveValueOrOffset`: `0` for
   `e.Type.FixedSize()` types, `uint64(len(e.Value))` only for
   `e.Type.Variable()` types (`mediatree/type.go`'s `FixedSize()`/
   `Variable()` are the discriminator already used elsewhere in this
   codebase for exactly this fixed-vs-variable branch, e.g.
   `toc/build.go:98`).

## Tests

- `internal/api/toctable_test.go` (new): `TestHandleReadTOCRows` (ready
  path — build a small fblock via the same fixture pattern
  `toc/build_test.go:42`'s `TestBuildMatchesDocWorkedExample` uses, assert
  row count/order/field values match the worked example directly),
  `TestHandleReadTOCRows_UnknownStorage` (mirroring
  `fblocktree_test.go:106`).
- `internal/api/toclivewsrows_test.go` (new), mirroring
  `fblocklivetree_test.go`'s existing cases 1:1 for the new route:
  `_UnknownStorage`, `_NoIngestManager`, `_IndexOutOfRange`,
  `_NotInProgress`, `_ConnectsAndSendsEmptyRowsWhenNoIngestData`.
- `toc/build_test.go` or a new `toc/inline_test.go`: if `packInline`/
  `unpackInline` are exported, add a direct round-trip test independent of
  the full `toc.Build` pipeline.

## Verify

`go test ./internal/api/... ./toc/... ./mediatree/...`. Manual: `curl` the
new `.../toc/rows` endpoint against a `ready` fblock and diff row count
against the existing `.../tree` endpoint's node count; open a WS client
(or wait for issue 03's UI) against `.../toc/rows/ws` on an `in_progress`
fblock and confirm rows update roughly every 500ms while ingest is active.

## Comments
