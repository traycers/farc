import type { TreeNode } from '../api/fblockTree'

// renderTreeAsText serializes a TreeNode into the same GNU-`tree`-style
// ASCII-art FblockTree.tsx renders on screen (nodeLabel/TreeLines), but as a
// plain string with every node forced open -- there is no depth/collapse
// notion here at all, unlike the React component (settled via grilling,
// 2026-08-21: the txt export is always fully expanded).
export function renderTreeAsText(root: TreeNode, formatValue?: (node: TreeNode) => string | undefined): string {
  const lines: string[] = []
  walk(root, '', true, true, formatValue, lines)
  return lines.join('\n')
}

function nodeLabel(node: TreeNode, formatValue?: (node: TreeNode) => string | undefined): string {
  const parts = [node.role, `(${node.type})`]
  if (node.size !== undefined) {
    parts.push(`size=${node.size}`)
  } else {
    const v = formatValue?.(node) ?? node.value
    if (v) parts.push(`= ${v}`)
  }
  return parts.join(' ')
}

function walk(
  node: TreeNode,
  prefix: string,
  isLast: boolean,
  isRoot: boolean,
  formatValue: ((node: TreeNode) => string | undefined) | undefined,
  lines: string[],
): void {
  const connector = isRoot ? '' : isLast ? '└── ' : '├── '
  lines.push(prefix + connector + nodeLabel(node, formatValue))

  const childPrefix = prefix + (isRoot ? '' : isLast ? '    ' : '│   ')
  const children = node.children ?? []
  children.forEach((child, i) => walk(child, childPrefix, i === children.length - 1, false, formatValue, lines))
}
