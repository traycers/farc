const BASE = '/api/farcd'

// Mirrors internal/api/fblocktree.go's TreeNode -- a structure-only view (no
// mediatree.NodeType.Variable() node ever carries its actual payload bytes,
// only Size). Value is always a decimal string (Timestamp/Duration are raw,
// unformatted unix-ns/ns -- this module formats them for display).
export type TreeNode = {
  id: number
  parent_id: number
  type: string
  role: string
  value?: string
  size?: number
  children?: TreeNode[]
}

// Mirrors internal/api/catalog.go's catalogEntry -- one row per fblock,
// covering the whole storage in a single request (fblocks-status-grid's
// initial load; per-index refresh after a live event uses getFblockInfo
// instead, see FblocksGridPage).
export type CatalogEntry = {
  index: number
  state: string
  uuid?: string
  begin?: string
  end?: string
  protected?: boolean
}

export async function getCatalog(storageId: string): Promise<CatalogEntry[]> {
  return (await ok(await fetch(`${BASE}/storages/${storageId}/catalog`))).json()
}

// Mirrors internal/api/fblocktree.go's fblockInfo.
export type FblockInfo = {
  index: number
  state: string
  uuid?: string
  begin?: string
  end?: string
  protected?: boolean
}

// Mirrors internal/api/fblocktree.go's fblockLiveTreeMessage -- the
// fblock-tree page's per-fblock live-data frame shape for a currently
// in_progress fblock. Tree is undefined if IngestManager hasn't observed
// any content for this Storage's shared segment yet.
export type FblockLiveTreeMessage = {
  tree?: TreeNode
}

async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

export async function getFblockTree(storageId: string, uuid: string): Promise<TreeNode | null> {
  return (await ok(await fetch(`${BASE}/storages/${storageId}/fcontainers/${uuid}/tree`))).json()
}

export async function getFblockInfo(storageId: string, index: number): Promise<FblockInfo> {
  return (await ok(await fetch(`${BASE}/storages/${storageId}/fblocks/${index}`))).json()
}

const RECONNECT_MIN_MS = 1000
const RECONNECT_MAX_MS = 15000

// subscribeFblockLiveTree opens the fblock-tree page's per-fblock live WS
// endpoint for a currently in_progress fblock. Unlike api/events.ts's
// subscribeJournal, there is no client subscribe handshake -- storageId/
// index are already in the URL path, and the server starts pushing
// immediately on connect. Every message is a full snapshot (no incremental
// append/reset framing), so a reconnect simply gets the current state
// again, never a stale partial tree.
export function subscribeFblockLiveTree(
  storageId: string,
  index: number,
  onMessage: (msg: FblockLiveTreeMessage) => void,
  onStatusChange?: (connected: boolean) => void,
): () => void {
  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = RECONNECT_MIN_MS
  let stopped = false

  function connect() {
    if (stopped) return
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(`${scheme}//${location.host}${BASE}/storages/${storageId}/fblocks/${index}/tree/ws`)

    ws.onopen = () => {
      reconnectDelay = RECONNECT_MIN_MS
      onStatusChange?.(true)
    }
    ws.onmessage = (ev) => {
      try {
        onMessage(JSON.parse(ev.data as string) as FblockLiveTreeMessage)
      } catch {
        // ignore a malformed frame
      }
    }
    ws.onclose = () => {
      onStatusChange?.(false)
      if (stopped) return
      reconnectTimer = setTimeout(connect, reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX_MS)
    }
    ws.onerror = () => ws?.close()
  }

  connect()

  return () => {
    stopped = true
    if (reconnectTimer) clearTimeout(reconnectTimer)
    ws?.close()
  }
}

// formatDurationNs renders a nanosecond duration for display (TypeDuration
// nodes) -- api/ns.ts's Timestamp helpers (nsToDate et al.) handle
// TypeTimestamp instead, since those are absolute unix-ns instants, not
// durations.
export function formatDurationNs(ns: bigint): string {
  if (ns < 1_000n) return `${ns} ns`
  if (ns < 1_000_000n) return `${Number(ns) / 1_000} µs`
  if (ns < 1_000_000_000n) return `${Number(ns) / 1_000_000} ms`
  return `${Number(ns) / 1_000_000_000} s`
}
