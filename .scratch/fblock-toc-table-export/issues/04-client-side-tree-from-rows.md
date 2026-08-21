# 04 — Build the fblock tree client-side from /toc/rows, delete /tree and /tree/ws

Status: resolved

## Task

`FblockTreePage` is the only caller of `/tree`/`/tree/ws`
(`internal/api/fblocktree.go`'s `handleReadFblockTree`/
`handleFblockLiveTreeWS`) — confirmed by search, no other Go package, e2e
test, or doc references them (spec decision 13). Those handlers exist only
to convert `toc.Columns`/`[]mediatree.Element` into a nested `TreeNode`
JSON tree; issue 02's `/toc/rows`/`/toc/rows/ws` already expose the exact
same underlying data flat, with no such conversion. This issue moves tree
construction to the client (so `FblockTreePage` fetches rows like
`FblockTocTablePage` already does) and deletes the now-dead
tree-building code.

### New client-side tree builder (`web/src/api/tocRowsToTree.ts`, new file)

1. `tocRowsToTree(rows: TocRow[]): TreeNode | null` — reconstructs the
   exact `TreeNode` shape (`id`, `parent_id`, `type`, `role`, `value?`,
   `size?`, `children?`) `handleReadFblockTree`/`handleFblockLiveTreeWS`
   used to send, so every existing consumer of that shape needs zero
   changes (spec decision 14): `FblockTree.tsx` (rendering),
   `frameGrouping.ts` (grouping), `fblockTreeFormat.ts` (value decoding),
   `fblockTreeText.ts`'s `renderTreeAsText` (issue 01's txt export).
2. Root: the row with `id === parent_id` (self-reference convention,
   `mediatree/node.go:19`/`toc/build.go:63-69` — same as
   `TreeNode`'s existing root convention, `fblocktree.go:27`). Return
   `null` for an empty row array (mirrors `buildColumnsTree`'s `c.N == 0`
   case, `fblocktree.go:97-99`).
3. Children: build a `Map<id, TreeNode>` in one pass, then a second pass
   appending each non-root row into its parent's `children` array, in row
   order. No sorting needed either way: ready rows are already
   DFS-preorder (a parent's children are contiguous in that order,
   `toc/query.go:23-31`'s `Children` doc), live rows are already creation
   order (`Filler.append`, `internal/fcontainer/filler.go:70-79` —
   `lastChild` tracks append order per parent) — both already yield
   correct child order via plain iteration, unlike `buildLiveTree`
   (`fblocktree.go:197-216`), which needed an explicit `kids` map only
   because it built the tree from a flat pre-reorder slice without one.
4. `value`/`size` per node: needs to know whether `row.type` is
   fixed-width (show `value = row.value_or_offset`, verbatim — already a
   decimal string) or variable-width (show `size = row.size`, omit
   `value`) — mirrors `columnsNode`/`liveNode`'s branch
   (`fblocktree.go:105-111`, `184-189`). Per spec decision 15, hardcode
   `const VARIABLE_TYPES = new Set(['string', 'bytes'])` in this module
   rather than adding a field to `TocRow` — `type` is a closed set
   (`docs/docs/archive/05-data-format.md` §3.1), and `fblockTreeFormat.ts`
   already hardcodes similar backend-enum knowledge for display.

### Migrate `FblockTreePage.tsx`

5. Replace `getFblockTree`/`subscribeFblockLiveTree` calls
   (`FblockTreePage.tsx:34-52`, current numbering) with
   `getFblockTocRows`/`subscribeFblockLiveTocRows` (already built for
   issue 03, `web/src/api/fblockTree.ts`), then `setTree(tocRowsToTree(rows))`
   instead of setting the fetched/streamed tree directly. Everything
   downstream (`formatValue`, `groupFrameNodes`, the "Download tree
   (.txt)" button, the "TOC table" nav link, `<FblockTree>` itself) is
   untouched — they all operate on the `tree`/`displayTree` state exactly
   as before, agnostic to how it was constructed.
6. `FblockTreePage.test.tsx`'s `vi.mock('../api/fblockTree', ...)` needs
   to mock `getFblockTocRows` (returning a flat `TocRow[]` fixture)
   instead of `getFblockTree` (returning a nested `TreeNode` fixture) —
   including the existing 150-video-frame-run fixture used to assert the
   txt download bypasses `frameGrouping.ts` (`FblockTreePage.test.tsx`'s
   "download tree button" describe block) — rewrite it as 150 flat rows
   sharing one `parent_id`.

### Delete dead backend code (`internal/api/fblocktree.go`, `server.go`)

7. Delete: `TreeNode` struct (`fblocktree.go:32-40`), `formatNodeValue`
   (`50-92`), `buildColumnsTree`/`columnsNode` (`94-116`),
   `handleReadFblockTree` (`120-131`), `liveNode`/`buildLiveTree`
   (`176-216`), `fblockLiveTreeMessage` (`225-227`),
   `buildFblockLiveTreeMessage` (`240-250`), `handleFblockLiveTreeWS`
   (`260-311`).
8. **Do not delete** — `toctable.go`'s live handler
   (`handleFblockLiveTOCRowsWS`) reuses them: `fblockLiveSig`
   (`fblocktree.go:231-234`), `liveTreeUpgrader` (`236`),
   `liveTreePollInterval` (`238`). Also keep `fblockInfo`/`handleGetFblock`
   (`137-174`) — both `FblockTreePage` and `FblockTocTablePage` resolve
   fblock state through it, independent of tree-vs-rows format. Consider
   (not required) moving the three kept live-WS helpers into
   `toctable.go`, since `fblocktree.go` no longer has a live-WS handler of
   its own to justify hosting them.
9. Remove the two routes from `server.go`: `GET
   /storages/{id}/fcontainers/{uuid}/tree` (line 194, current numbering)
   and `GET /storages/{id}/fblocks/{index}/tree/ws` (line 200).
10. Trim `internal/api/fblocktree_test.go`: delete `TestFormatNodeValue`,
    `TestHandleReadFblockTree`, `TestHandleReadFblockTree_UnknownStorage`
    (tied to deleted code) — **keep** `TestHandleGetFblock`/
    `_OutOfRange` (handler survives). Delete
    `internal/api/fblocklivetree_test.go` entirely (every test in it
    targets `handleFblockLiveTreeWS`).

### Delete now-unreachable frontend exports

11. From `web/src/api/fblockTree.ts`, delete `getFblockTree`,
    `subscribeFblockLiveTree`, `FblockLiveTreeMessage`. **Keep** the
    `TreeNode` type itself — it's still the shape `tocRowsToTree`
    produces and every tree-rendering module consumes.

## Tests

`web/src/api/tocRowsToTree.test.ts` (new): builds a `TreeNode` matching
`toc/build_test.go`'s worked-example shape (reuse the same small
row-fixture style already used in `internal/api/toctable_test.go`'s
`TestTocRowsFromColumns`/`TestTocRowsFromElements`, translated to
`TocRow[]`) — asserts children order, root detection via self-reference,
and the fixed-vs-variable `value`/`size` branch for both a `uint32` and a
`bytes` row. `FblockTreePage.test.tsx`: update existing tests' mocks per
step 6; all prior assertions (formatValue decoding, download-tree button,
TOC-table link) should pass unchanged once the mock shape is fixed.
`internal/api` Go tests: `fblocktree_test.go` trimmed per step 10;
`internal/api/toctable_test.go`/`toclivewsrows_test.go` untouched (they
don't reference anything being deleted).

## Verify

`go build ./... && go vet ./... && go test ./...`, `golangci-lint run` —
whole repo, confirms nothing else referenced the deleted symbols. `cd web
&& npx vitest run` and `task build/web`. Manually: open a `ready` and an
`in_progress` fblock's `/tree` page, confirm the rendered tree, the "TOC
table" link, and the "Download tree (.txt)" button all behave exactly as
before the migration; confirm `curl .../fcontainers/{uuid}/tree` now
404s (route removed).

## Comments

2026-08-21: Implemented via TDD. Added `web/src/api/tocRowsToTree.ts`
(`tocRowsToTree`), migrated `FblockTreePage.tsx` to
`getFblockTocRows`/`subscribeFblockLiveTocRows` + `tocRowsToTree` (steps
1-6); deleted `TreeNode`/`formatNodeValue`/`buildColumnsTree`/
`columnsNode`/`handleReadFblockTree`/`liveNode`/`buildLiveTree`/
`fblockLiveTreeMessage`/`buildFblockLiveTreeMessage`/
`handleFblockLiveTreeWS` from `internal/api/fblocktree.go` (now just
`fblockInfo`/`handleGetFblock`) and the two `/tree`/`/tree/ws` routes from
`server.go` (steps 7, 9); trimmed `fblocktree_test.go` to
`TestHandleGetFblock`/`_OutOfRange`, deleted `fblocklivetree_test.go`
entirely (step 10); deleted `getFblockTree`/`subscribeFblockLiveTree`/
`FblockLiveTreeMessage` from `fblockTree.ts`, kept `TreeNode` (step 11).
Per step 8, moved (rather than left in place) `fblockLiveSig`/
`liveTreeUpgrader`/`liveTreePollInterval` into `toctable.go`, since
`fblocktree.go` no longer has any live-WS handler to justify hosting
them. Updated `CONTEXT.md`'s fcontainer entry, which named the
now-deleted `handleFblockLiveTreeWS` as the current live-tree mechanism.

**Real bug caught by the first red test**: a naive `tocRowsToTree` would
show `value: "0"` on every `void`-typed node (channels, streams,
video/audio, configs — most structural nodes in a real tree), because
`void` is fixed-width (`FixedSize()==0`) so it fell into the "show value"
branch with `value_or_offset` packed to `0`. The deleted
`formatNodeValue`'s `TypeVoid` case always returned `""`, which
`omitempty` then dropped from the JSON entirely — the old tree never
showed a value for these nodes at all. Fixed by special-casing
`type === 'void'` to omit `value` too, matching the original behavior
exactly; a test with a non-first-position root (`id !== 0` in array order)
also caught an early temptation to assume root is always `rows[0]`, driving
the self-reference (`id === parent_id`) lookup instead.

`TestHandleReadTOCRows` (issue 02) cross-checked row count against the
now-deleted `/tree` endpoint's node count — rewritten to check directly
against `unit.ReadTOC(uuid)`'s `*toc.Columns.N`, the actual source of
truth this handler reads (arguably a better assertion than the one it
replaced).

`go build/vet/test ./...` and `golangci-lint run` (whole repo) — clean.
`cd web && npx vitest run` — 196 passed. `task build/web` — green (same
pre-existing >500kB chunk-size warning, unrelated).
