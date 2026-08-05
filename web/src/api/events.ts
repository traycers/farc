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

const RECONNECT_MIN_MS = 1000
const RECONNECT_MAX_MS = 15000

// subscribeJournal opens a "global" (storage: '') subscription to farcd's
// EventPushServer -- unfiltered, so every event kind reaches onEvent.
// farcd does no reconnect catch-up (documented v1 limitation), so a
// disconnect gap simply loses events; this only reconnects the socket, it
// does not replay anything missed while down. Returns a cleanup function
// that closes the socket and cancels any pending reconnect.
export function subscribeJournal(
  onEvent: (e: JournalEvent) => void,
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
      ws?.send(JSON.stringify({ storage: '', want: [], channels: [] }))
    }
    ws.onmessage = (ev) => {
      try {
        onEvent(JSON.parse(ev.data as string) as JournalEvent)
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
