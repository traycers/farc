# 01 — Download expanded tree as .txt

Status: open

## Task

Add a "Download tree (.txt)" button to `FblockTreePage.tsx` that saves the
*raw*, fully-expanded tree as an ASCII-art text file — no backend changes
needed, this is purely a client-side serialization of data the page already
has. See spec decisions 10-12 for why raw (un-grouped) and why this button
lives on `FblockTreePage`, not the tree component.

### New pure serializer (`web/src/pages/fblockTreeText.ts`, new file)

1. `FblockTree.tsx`'s `TreeLines` (lines 42-100) is JSX-only, coupled to
   `mode`/`manual` collapse state (`FblockTree.tsx:111-112`) and returns
   React elements — there is no existing plain-string tree serializer to
   reuse. Write `renderTreeAsText(root: TreeNode, formatValue?: (node:
   TreeNode) => string): string`, mirroring the connector/prefix math at
   `FblockTree.tsx:65-66` (`isLast ? '└── ' : '├── '`,
   `prefix + (isLast ? '    ' : '│   ')`) and the label logic at
   `FblockTree.tsx:27-36` (`nodeLabel`, which calls the `formatValue` prop —
   reuse `FblockTreePage.tsx:9-13`'s exported `formatValue` function as the
   caller-supplied formatter, same as `FblockTreePage.tsx:80` does for the
   on-screen render).
2. Every node is emitted (fully expanded) regardless of any
   collapse/expand state — this function takes no `mode`/`manual`
   parameters at all, unlike `TreeLines`.
3. Operates on the *raw* `tree` (`FblockTreePage.tsx:24`'s state), never on
   `displayTree` (`FblockTreePage.tsx:54`, the `groupFrameNodes(tree)`
   result) — per spec decision 10, so every line corresponds to a real
   TOC/live-tree node, not a synthetic `frameGrouping.ts` group node.
   Placed in `web/src/pages/`, not `web/src/api/` — matching the existing
   `fblockTreeFormat.ts`/`fblocksListPaging.ts` precedent of colocating a
   page's pure display-logic helpers there, since nothing in this module
   talks to the backend.

### New download helper (`web/src/lib/download.ts`, new file, new directory)

4. No blob/`createObjectURL`/`<a download>` pattern exists anywhere in
   `web/src` today (confirmed by grep — from-scratch addition), and no
   generic shared-utility directory exists either. Add a new
   `web/src/lib/` for this kind of cross-cutting, non-page, non-API
   helper — `download.ts` is the first file in it. Export
   `downloadTextFile(filename: string, content: string, mimeType =
   'text/plain'): void` — `new Blob([content], {type: mimeType})` +
   `URL.createObjectURL` + a hidden `<a download>` click + `URL.revokeObjectURL`
   cleanup. Issue 03 reuses this same helper for the CSV download, so land
   this issue first (per spec's issue ordering) or keep the helper trivial
   enough to not create merge friction.

### Wire it into `FblockTreePage.tsx`

5. Add a button in the header row (`FblockTreePage.tsx:58-65`, alongside
   the existing "Back" button) or a small toolbar line just above the tree
   container (`FblockTreePage.tsx:79`). Enabled whenever `tree` is non-null
   (already true for both the `ready` static-fetch path,
   `FblockTreePage.tsx:37`, and the `in_progress` live-WS path,
   `FblockTreePage.tsx:50` — no new state-gating needed).
6. On click: `downloadTextFile(filename, renderTreeAsText(tree, formatValue))`.
   Filename: `fblock-${info.uuid ?? info.index}-tree.txt` — use the uuid
   when available (`ready` state), fall back to the physical index
   (`in_progress`, no uuid yet).

## Tests

- `web/src/pages/fblockTreeText.test.ts` (new): unit-test `renderTreeAsText`
  against a hand-built `TreeNode` fixture (the same `deepChain()`-style
  fixture `FblockTree.test.tsx` already uses for its 7-level chain) —
  assert exact connector characters for a multi-child, multi-depth tree,
  and that every node appears in the output regardless of any
  depth/collapse-like input (there is none — the function has no such
  parameter, which the test should make an explicit assertion about by
  using a tree deeper than `FblockTree.tsx`'s `DEFAULT_OPEN_DEPTH = 5`,
  line 20, and confirming all nodes still appear).
- `web/src/lib/download.test.ts` (new): mock `URL.createObjectURL`/
  `revokeObjectURL`, assert `downloadTextFile` constructs a `Blob` with the
  given content/mime type and triggers a click on an anchor with the given
  filename.
- `FblockTreePage.test.tsx`: extend to assert the new button calls
  `downloadTextFile` with `renderTreeAsText(tree, ...)` (raw, un-grouped)
  rather than `displayTree` — the regression this issue must guard against
  is someone later wiring the button to the grouped tree by mistake.

## Verify

`cd web && npm test`; `task build/web`. Manually: open a `ready` fblock's
tree page with enough frames to trigger `frameGrouping.ts`'s collapsing
(≥100 video frames), click "Download tree (.txt)", confirm the file
contains one line per real frame (no `"N frames"` group line) and is fully
expanded. Repeat against an `in_progress` fblock mid-live-stream.

## Comments
