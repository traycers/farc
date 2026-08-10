import { useEffect, useState } from 'react'
import { getFblockTreeNode, type TreeNode } from '../api/fblocktree'
import type { LiveNode } from '../api/events'

// Roles whose individual instances are never shown as their own tree row --
// there can be millions of them (docs/docs/archive/08-array-trees.md §3.4),
// so both modes collapse them into a single "N кадров" summary under their
// "frames(video)"/"frames(audio)" parent instead.
const FRAME_ROLES = new Set(['frame(video)', 'frame(audio)'])
const FRAME_LEAF_ROLES = new Set([
  'frame_data(video)',
  'frame_data(audio)',
  'frame_time(video)',
  'frame_time(audio)',
  'frame_kind',
])
const FRAMES_CONTAINER_ROLES = new Set(['frames(video)', 'frames(audio)'])

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

// nsToLocalDisplay renders a decimal-string ns count as a local date-time --
// duplicated (not imported) from web/src/api/ns.ts's nsToDisplayString
// because that one takes a bigint, and re-parsing a decimal string into a
// bigint just to immediately reformat it is simple enough to inline here.
function formatTimestamp(nsDecimal: string): string {
  const ns = BigInt(nsDecimal)
  const d = new Date(Number(ns / 1_000_000n))
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function formatValue(type: string, value: string | number): string {
  if (type === 'timestamp') return formatTimestamp(String(value))
  if (type === 'duration') return `${value} ns`
  return String(value)
}

function nodeLabel(role: string, type: string, value: string | number | undefined, size: number | undefined): string {
  if (value !== undefined) return `${role}: ${formatValue(type, value)}`
  if (size !== undefined) return `${role} — ${formatBytes(size)}`
  return role
}

type RowProps = {
  prefix: string
  isLast: boolean
  label: string
  isNew?: boolean
  expandable?: boolean
  expanded?: boolean
  onToggle?: () => void
  loading?: boolean
}

function TreeRow({ prefix, isLast, label, isNew, expandable, expanded, onToggle, loading }: RowProps) {
  const connector = isLast ? '└── ' : '├── '
  const arrow = expandable ? (expanded ? '▾ ' : '▸ ') : ''
  return (
    <div className={`tree-row${isNew ? ' tree-node--new' : ''}${expandable ? ' tree-row--expandable' : ''}`} onClick={onToggle}>
      <span className="tree-guide">{prefix + connector}</span>
      <span className="tree-label">
        {arrow}
        {label}
        {loading && ' …'}
      </span>
    </div>
  )
}

function childPrefix(prefix: string, isLast: boolean): string {
  return prefix + (isLast ? '    ' : '│   ')
}

// ---- status mode: lazy, paginated fetch from GET .../tree ----

type NodeState = {
  children: TreeNode[]
  total: number
  loading: boolean
}

function StatusChildren({
  storage,
  uuid,
  nodes,
  prefix,
  states,
  expanded,
  onExpand,
  onLoadMore,
}: {
  storage: string
  uuid: string
  nodes: TreeNode[]
  prefix: string
  states: Map<number, NodeState>
  expanded: Set<number>
  onExpand: (id: number) => void
  onLoadMore: (id: number) => void
}) {
  return (
    <>
      {nodes.map((n, i) => {
        const isLast = i === nodes.length - 1
        const expandable = n.child_count > 0
        const isOpen = expanded.has(n.id)
        const st = states.get(n.id)
        const label = FRAMES_CONTAINER_ROLES.has(n.role)
          ? `${n.role} [${n.child_count} кадров]`
          : nodeLabel(n.role, n.type, n.value, n.size)
        return (
          <div key={n.id}>
            <TreeRow
              prefix={prefix}
              isLast={isLast}
              label={label}
              expandable={expandable}
              expanded={isOpen}
              loading={st?.loading}
              onToggle={expandable ? () => onExpand(n.id) : undefined}
            />
            {isOpen && st && (
              <>
                <StatusChildren
                  storage={storage}
                  uuid={uuid}
                  nodes={st.children}
                  prefix={childPrefix(prefix, isLast)}
                  states={states}
                  expanded={expanded}
                  onExpand={onExpand}
                  onLoadMore={onLoadMore}
                />
                {st.children.length < st.total && (
                  <div className="tree-row tree-row--expandable" onClick={() => onLoadMore(n.id)}>
                    <span className="tree-guide">{childPrefix(prefix, isLast)}</span>
                    <span className="tree-label">
                      … ещё {st.total - st.children.length} (загружено {st.children.length} из {st.total})
                      {st.loading && ' …'}
                    </span>
                  </div>
                )}
              </>
            )}
          </div>
        )
      })}
    </>
  )
}

const CHILD_PAGE_LIMIT = 500

function FblockStatusTree({ storage, uuid }: { storage: string; uuid: string }) {
  const [root, setRoot] = useState<TreeNode | null>(null)
  const [rootChildren, setRootChildren] = useState<TreeNode[]>([])
  const [rootTotal, setRootTotal] = useState(0)
  const [states, setStates] = useState<Map<number, NodeState>>(new Map())
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setRoot(null)
    setRootChildren([])
    setStates(new Map())
    setExpanded(new Set())
    setError(null)
    getFblockTreeNode(storage, uuid, undefined, { limit: CHILD_PAGE_LIMIT })
      .then((lvl) => {
        setRoot(lvl.node)
        setRootChildren(lvl.children)
        setRootTotal(lvl.total)
      })
      .catch((e) => setError(String(e)))
  }, [storage, uuid])

  function loadLevel(id: number, offset: number) {
    setStates((prev) => {
      const next = new Map(prev)
      const cur = next.get(id) ?? { children: [], total: 0, loading: false }
      next.set(id, { ...cur, loading: true })
      return next
    })
    getFblockTreeNode(storage, uuid, id, { offset, limit: CHILD_PAGE_LIMIT })
      .then((lvl) => {
        setStates((prev) => {
          const next = new Map(prev)
          const cur = next.get(id) ?? { children: [], total: 0, loading: false }
          next.set(id, { children: [...cur.children, ...lvl.children], total: lvl.total, loading: false })
          return next
        })
      })
      .catch((e) => setError(String(e)))
  }

  function onExpand(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev)
      next.add(id)
      return next
    })
    if (!states.has(id)) loadLevel(id, 0)
  }

  function onLoadMore(id: number) {
    const cur = states.get(id)
    loadLevel(id, cur?.children.length ?? 0)
  }

  if (error) return <div className="alert alert-danger">{error}</div>
  if (!root) return <div className="text-muted">Загрузка…</div>

  return (
    <div className="tree">
      <div className="tree-row">
        <span className="tree-label">{nodeLabel(root.role, root.type, root.value, root.size)}</span>
      </div>
      <StatusChildren
        storage={storage}
        uuid={uuid}
        nodes={rootChildren}
        prefix=""
        states={states}
        expanded={expanded}
        onExpand={onExpand}
        onLoadMore={onLoadMore}
      />
      {rootChildren.length < rootTotal && <div className="text-muted small">… ещё {rootTotal - rootChildren.length}</div>}
    </div>
  )
}

// ---- live mode: fully in-memory, pushed via WS ----

export type LiveTreeState = {
  nodesById: Map<number, LiveNode>
  frameCounts: Map<number, number>
  newIds: Set<number>
}

export function newLiveTreeState(): LiveTreeState {
  return { nodesById: new Map(), frameCounts: new Map(), newIds: new Set() }
}

// applyLiveNodes folds a live-progress WS delta into state, returning a new
// LiveTreeState (state is treated as immutable, matching React's expected
// update pattern). Frame-role nodes are aggregated into frameCounts rather
// than stored individually -- see FRAME_ROLES/FRAME_LEAF_ROLES above.
export function applyLiveNodes(state: LiveTreeState, nodes: LiveNode[]): LiveTreeState {
  const nodesById = new Map(state.nodesById)
  const frameCounts = new Map(state.frameCounts)
  const newIds = new Set(state.newIds)
  for (const n of nodes) {
    if (FRAME_ROLES.has(n.role)) {
      frameCounts.set(n.parent, (frameCounts.get(n.parent) ?? 0) + 1)
      continue
    }
    if (FRAME_LEAF_ROLES.has(n.role)) continue
    nodesById.set(n.id, n)
    newIds.add(n.id)
  }
  return { nodesById, frameCounts, newIds }
}

// clearNewIds drops ids from state.newIds (a green highlight is transient --
// FblockLivePage calls this a few seconds after applyLiveNodes added them,
// letting index.css's transition fade the color back down).
export function clearNewIds(state: LiveTreeState, ids: number[]): LiveTreeState {
  if (ids.length === 0) return state
  const newIds = new Set(state.newIds)
  for (const id of ids) newIds.delete(id)
  return { ...state, newIds }
}

function LiveChildren({
  ids,
  prefix,
  state,
}: {
  ids: number[]
  prefix: string
  state: LiveTreeState
}) {
  return (
    <>
      {ids.map((id, i) => {
        const n = state.nodesById.get(id)
        if (!n) return null
        const isLast = i === ids.length - 1
        const isFramesContainer = FRAMES_CONTAINER_ROLES.has(n.role)
        const label = isFramesContainer
          ? `${n.role} [${state.frameCounts.get(id) ?? 0} кадров]`
          : nodeLabel(n.role, n.type, n.value, n.size)
        const childIds = isFramesContainer
          ? []
          : [...state.nodesById.values()].filter((c) => c.parent === id && c.id !== id).map((c) => c.id)
        return (
          <div key={id}>
            <TreeRow prefix={prefix} isLast={isLast} label={label} isNew={state.newIds.has(id)} />
            <LiveChildren ids={childIds} prefix={childPrefix(prefix, isLast)} state={state} />
          </div>
        )
      })}
    </>
  )
}

function FblockLiveTree({ state }: { state: LiveTreeState }) {
  const root = state.nodesById.get(0)
  if (!root) return <div className="text-muted">Ожидание данных…</div>
  const childIds = [...state.nodesById.values()].filter((n) => n.parent === 0 && n.id !== 0).map((n) => n.id)
  return (
    <div className="tree">
      <div className="tree-row">
        <span className="tree-label">{root.role}</span>
      </div>
      <LiveChildren ids={childIds} prefix="" state={state} />
    </div>
  )
}

// ---- public component ----

export type FblockTreeProps = { mode: 'status'; storage: string; uuid: string } | { mode: 'live'; state: LiveTreeState }

export default function FblockTree(props: FblockTreeProps) {
  if (props.mode === 'status') return <FblockStatusTree storage={props.storage} uuid={props.uuid} />
  return <FblockLiveTree state={props.state} />
}
