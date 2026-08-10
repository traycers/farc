const WS_BASE = '/api/events'

// Mirrors farcd's internal/api/eventpush.go pushMessage JSON shape --
// index/uuid/severity/reason are only set for fblock.* events, channel only
// for channel/recording/trigger events.
export type JournalEvent = {
  type: string
  name: string
  index?: number
  uuid?: string
  severity?: string
  reason?: string
  channel?: number
  storage?: string
}

// Mirrors internal/api/eventpush.go's liveNode/livePushMessage -- the
// fblock-live page's per-tick growth of a channel's segment still being
// recorded (not yet on disk). Value/size follow the same convention as
// web/src/api/fblocktree.ts's TreeNode (a decimal string for ns-scale
// values, a plain number otherwise); unlike TreeNode there's no child_count,
// since this is a flat delta list, not a "node + its children" listing.
export type LiveNode = {
  id: number
  role: string
  type: string
  parent: number
  value?: string | number
  size?: number
}

export type LivePushMessage = {
  type: 'live'
  storage: string
  channel: number
  total: number
  content_bytes: number
  estimated_toc_bytes: number
  nodes: LiveNode[]
}

const RECONNECT_MIN_MS = 1000
const RECONNECT_MAX_MS = 15000

// connectEvents opens farcd's EventPushServer WS endpoint, sends body on
// open, and forwards every parsed frame to onMessage -- the reconnect/
// backoff loop shared by subscribeJournal and subscribeLive. farcd does no
// reconnect catch-up (documented v1 limitation): a disconnect gap simply
// loses events; this only reconnects the socket, it does not replay
// anything missed while down. Returns a cleanup function that closes the
// socket and cancels any pending reconnect.
function connectEvents(
  body: unknown,
  onMessage: (msg: { type?: string }) => void,
  onStatusChange?: (connected: boolean) => void,
): () => void {
  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = RECONNECT_MIN_MS
  let stopped = false

  function connect() {
    if (stopped) return
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:'
    ws = new WebSocket(`${scheme}//${location.host}${WS_BASE}/ws`)

    ws.onopen = () => {
      reconnectDelay = RECONNECT_MIN_MS
      onStatusChange?.(true)
      ws?.send(JSON.stringify(body))
    }
    ws.onmessage = (ev) => {
      try {
        onMessage(JSON.parse(ev.data as string))
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

// subscribeJournal opens a "global" (storage: '') subscription to farcd's
// EventPushServer -- unfiltered, so every event kind reaches onEvent.
export function subscribeJournal(
  onEvent: (e: JournalEvent) => void,
  onStatusChange?: (connected: boolean) => void,
): () => void {
  return connectEvents({ storage: '', want: [], channels: [] }, (msg) => onEvent(msg as JournalEvent), onStatusChange)
}

// subscribeLive opens a "global" subscription scoped to one channel via
// subscribeMessage.Channels -- required for "live" frames to be delivered
// at all (internal/api/eventpush.go's serveGlobal treats an empty Channels
// set as "no live subscriber", since unscoped live progress is meaningless).
// Classic events (fblock.created/fblock.ready, needed to know when to
// switch from live progress to the finalized tree) are NOT filtered by
// channel server-side, so onEvent is called for every one and the caller
// must check its own .channel/.storage fields.
export function subscribeLive(
  channel: number,
  onLive: (msg: LivePushMessage) => void,
  onEvent: (e: JournalEvent) => void,
  onStatusChange?: (connected: boolean) => void,
): () => void {
  return connectEvents(
    { storage: '', want: [], channels: [channel] },
    (msg) => {
      if (msg.type === 'live') {
        onLive(msg as LivePushMessage)
      } else {
        onEvent(msg as JournalEvent)
      }
    },
    onStatusChange,
  )
}
