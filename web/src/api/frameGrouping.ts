import type { TreeNode } from './fblockTree'

// THRESHOLDS maps a frame node's Role (mediatree.Role.String() wire value)
// to the minimum run length of consecutive siblings of that role that
// collapses into one group node (settled via grilling, 2026-08-14): 100 for
// video, 500 for audio -- these trees can otherwise have thousands of frame
// nodes, making them impractical to read.
const THRESHOLDS: Record<string, number> = {
  'frame(video)': 100,
  'frame(audio)': 500,
}

// groupFrameNodes walks root and replaces every run of >= THRESHOLDS[role]
// consecutive sibling frame nodes with one synthetic "group" TreeNode
// wrapping them as its own children -- purely a client-side display
// transform (farcd never sees this), applied fresh to each fetched/streamed
// tree. Group node ids are negative, so they never collide with the
// server's own (always non-negative) ids.
export function groupFrameNodes(root: TreeNode): TreeNode {
  return groupNode(root, { next: -1 })
}

function groupNode(node: TreeNode, counter: { next: number }): TreeNode {
  const children = node.children
  if (!children || children.length === 0) return node

  const grouped: TreeNode[] = []
  let i = 0
  while (i < children.length) {
    const role = children[i].role
    const threshold = THRESHOLDS[role]
    if (threshold !== undefined) {
      let j = i
      while (j < children.length && children[j].role === role) j++
      const runLength = j - i
      if (runLength >= threshold) {
        grouped.push(makeGroup(role, children.slice(i, j), counter))
        i = j
        continue
      }
    }
    grouped.push(groupNode(children[i], counter))
    i++
  }
  return { ...node, children: grouped }
}

function makeGroup(role: string, members: TreeNode[], counter: { next: number }): TreeNode {
  return {
    id: counter.next--,
    parent_id: members[0].parent_id,
    type: 'group',
    role,
    value: `${members.length} frames`,
    children: members,
  }
}
