import { useState } from 'react'
import type { TreeNode } from '../api/fblockTree'

type FblockTreeProps = {
  root: TreeNode | null
  // Newly-arrived node ids (fblock-live's "append" batches) to highlight
  // green -- fblock-status never passes this (nothing is ever "new" there).
  newIds?: ReadonlySet<number>
  // Overrides the raw Value string for display (e.g. formatting a
  // "timestamp"/"duration" node's raw ns via api/ns.ts). Returning undefined
  // falls back to node.value as-is.
  formatValue?: (node: TreeNode) => string | undefined
}

// DEFAULT_OPEN_DEPTH: root = depth 0, so levels 0-4 (5 levels) are open by
// default (settled via grilling, 2026-08-14). A synthetic frame-group node
// (frameGrouping.ts's type: 'group') always starts closed regardless of
// depth -- showing its many members expanded by default would defeat the
// point of grouping them.
const DEFAULT_OPEN_DEPTH = 5

// ToggleMode is "expand all"/"collapse all"'s effect on every node that
// hasn't been individually clicked since -- manual per-node clicks (tracked
// in FblockTree's own `manual` map) always take precedence over this.
type ToggleMode = 'default' | 'all-open' | 'all-closed'

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

function defaultOpen(node: TreeNode, depth: number): boolean {
  return depth < DEFAULT_OPEN_DEPTH && node.type !== 'group'
}

function TreeLines({
  node,
  depth,
  prefix,
  isLast,
  isRoot,
  newIds,
  formatValue,
  mode,
  manual,
  onToggle,
}: {
  node: TreeNode
  depth: number
  prefix: string
  isLast: boolean
  isRoot: boolean
  newIds?: ReadonlySet<number>
  formatValue?: (node: TreeNode) => string | undefined
  mode: ToggleMode
  manual: ReadonlyMap<number, boolean>
  onToggle: (id: number, current: boolean) => void
}) {
  const connector = isRoot ? '' : isLast ? '└── ' : '├── '
  const childPrefix = prefix + (isRoot ? '' : isLast ? '    ' : '│   ')
  const isNew = newIds?.has(node.id) ?? false
  const children = node.children ?? []
  const hasChildren = children.length > 0
  const open = manual.has(node.id) ? manual.get(node.id)! : mode === 'all-open' ? true : mode === 'all-closed' ? false : defaultOpen(node, depth)
  return (
    <>
      <div
        className={isNew ? 'text-success fw-semibold' : undefined}
        style={hasChildren ? { cursor: 'pointer' } : undefined}
        onClick={hasChildren ? () => onToggle(node.id, open) : undefined}
      >
        <span style={{ whiteSpace: 'pre' }}>{prefix + connector}</span>
        {nodeLabel(node, formatValue)}
        {hasChildren ? ` [${open ? '-' : '+'}]` : ''}
      </div>
      {open &&
        children.map((child, i) => (
          <TreeLines
            key={child.id}
            node={child}
            depth={depth + 1}
            prefix={childPrefix}
            isLast={i === children.length - 1}
            isRoot={false}
            newIds={newIds}
            formatValue={formatValue}
            mode={mode}
            manual={manual}
            onToggle={onToggle}
          />
        ))}
    </>
  )
}

// FblockTree renders a fcontainer's node tree in a GNU-`tree`-style nested
// layout (├──/└──/│ connectors), shared by fblock-tree (read-only/live).
// newIds highlights nodes from the most recent WS "append". Nodes below
// DEFAULT_OPEN_DEPTH start collapsed; clicking any row with children
// toggles it (TreeLines already computed that row's current open value,
// so onToggle just flips it -- no need to re-derive state from depth/mode
// at click time), and "expand all"/"collapse all" reset every node not
// since individually clicked.
export default function FblockTree({ root, newIds, formatValue }: FblockTreeProps) {
  const [mode, setMode] = useState<ToggleMode>('default')
  const [manual, setManual] = useState<Map<number, boolean>>(new Map())

  if (!root) {
    return <div className="text-body-secondary">(empty tree)</div>
  }

  function toggle(id: number, current: boolean) {
    setManual((prev) => {
      const next = new Map(prev)
      next.set(id, !current)
      return next
    })
  }

  return (
    <div>
      <div className="mb-2 btn-group btn-group-sm">
        <button
          type="button"
          className="btn btn-outline-secondary"
          onClick={() => {
            setMode('all-open')
            setManual(new Map())
          }}
        >
          Expand all
        </button>
        <button
          type="button"
          className="btn btn-outline-secondary"
          onClick={() => {
            setMode('all-closed')
            setManual(new Map())
          }}
        >
          Collapse all
        </button>
      </div>
      <div className="font-monospace" style={{ fontSize: '0.9rem' }}>
        <TreeLines node={root} depth={0} prefix="" isLast isRoot newIds={newIds} formatValue={formatValue} mode={mode} manual={manual} onToggle={toggle} />
      </div>
    </div>
  )
}
