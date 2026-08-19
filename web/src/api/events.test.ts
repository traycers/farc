import { beforeEach, describe, expect, it, vi } from 'vitest'
import { subscribeStorageEvents } from './events'

// FakeWebSocket is a minimal test double for the browser WebSocket global --
// jsdom's own WebSocket can't actually connect, and there's no existing
// mock for it in this test suite yet. Captures what was sent and lets the
// test drive onopen/onmessage manually.
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(public url: string) {
    FakeWebSocket.instances.push(this)
  }
  send(data: string) {
    this.sent.push(data)
  }
  close() {}
}

beforeEach(() => {
  FakeWebSocket.instances.length = 0
  // @ts-expect-error -- test double, not a full WebSocket implementation
  globalThis.WebSocket = FakeWebSocket
})

describe('subscribeStorageEvents', () => {
  it('sends include_pool when requested, and routes "pool" frames to onPool instead of onEvent', () => {
    const onEvent = vi.fn()
    const onPool = vi.fn()
    const unsubscribe = subscribeStorageEvents('s1', [], onEvent, undefined, { includePool: true, onPool })

    const ws = FakeWebSocket.instances[0]
    ws.onopen?.()
    expect(JSON.parse(ws.sent[0])).toEqual({ storage: 's1', want: [], channels: [], include_pool: true })

    ws.onmessage?.({ data: JSON.stringify({ type: 'pool', storage: 's1', slots: [] }) })
    expect(onPool).toHaveBeenCalledWith({ type: 'pool', storage: 's1', slots: [] })
    expect(onEvent).not.toHaveBeenCalled()

    ws.onmessage?.({ data: JSON.stringify({ type: 'event', name: 'fblock.write.completed' }) })
    expect(onEvent).toHaveBeenCalledWith({ type: 'event', name: 'fblock.write.completed' })

    unsubscribe()
  })
})
