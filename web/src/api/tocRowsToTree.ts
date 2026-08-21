import type { TocRow, TreeNode } from './fblockTree'

// VARIABLE_TYPES mirrors mediatree.NodeType's variable-width subset
// (string, bytes) -- a closed set (docs/docs/archive/05-data-format.md
// §3.1, unlike the open, domain-growing `role` set), hardcoded here rather
// than added to the wire format, matching fblockTreeFormat.ts's existing
// precedent of duplicating backend enum knowledge for display.
const VARIABLE_TYPES = new Set(['string', 'bytes'])

function tocRowToNode(row: TocRow): TreeNode {
  const node: TreeNode = { id: row.id, parent_id: row.parent_id, type: row.type, role: row.role }
  if (VARIABLE_TYPES.has(row.type)) {
    node.size = row.size
  } else if (row.type !== 'void') {
    // void is fixed-width too (FixedSize()==0), but the deleted backend's
    // formatNodeValue always returned "" for it, which `omitempty` then
    // dropped from the JSON entirely -- most structural nodes (channels,
    // streams, video/audio, configs) are void, so showing their packed
    // "0" value_or_offset here would be a real, widely-visible regression
    // (e.g. "channels (void) = 0" where the old tree showed "channels (void)").
    node.value = row.value_or_offset
  }
  return node
}

// tocRowsToTree reconstructs the exact TreeNode shape farcd's now-removed
// /tree and /tree/ws endpoints used to send, from the flat rows /toc/rows
// and /toc/rows/ws already expose -- so FblockTree.tsx/frameGrouping.ts/
// fblockTreeFormat.ts/fblockTreeText.ts need no changes, only their data
// source does (settled via grilling, 2026-08-21). No sorting is needed
// either way: ready rows are already DFS-preorder (a parent's children are
// contiguous in that order) and live rows are already creation order
// (Filler.append tracks per-parent append order) -- both yield correct
// child order via plain iteration.
export function tocRowsToTree(rows: TocRow[]): TreeNode | null {
  if (rows.length === 0) return null

  const nodes = new Map<number, TreeNode>()
  for (const row of rows) {
    nodes.set(row.id, tocRowToNode(row))
  }

  let root: TreeNode | null = null
  for (const row of rows) {
    const node = nodes.get(row.id)!
    if (row.id === row.parent_id) {
      root = node
      continue
    }
    const parent = nodes.get(row.parent_id)
    if (!parent) continue
    parent.children = parent.children ? [...parent.children, node] : [node]
  }
  return root
}
