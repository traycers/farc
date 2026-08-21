import { afterEach, describe, expect, it, vi } from 'vitest'
import { connectWhep } from './whep'

class FakePeerConnection {
  transceivers: { kind: string; direction: string }[] = []
  localDescription: unknown
  remoteDescription: unknown
  ontrack: ((e: { streams: MediaStream[] }) => void) | null = null
  closed = false

  addTransceiver(kind: string, opts: { direction: string }) {
    this.transceivers.push({ kind, direction: opts.direction })
  }
  async createOffer() {
    return { type: 'offer' as const, sdp: 'fake-offer-sdp' }
  }
  async setLocalDescription(desc: unknown) {
    this.localDescription = desc
  }
  async setRemoteDescription(desc: unknown) {
    this.remoteDescription = desc
  }
  close() {
    this.closed = true
  }
}

describe('connectWhep', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('creates recvonly video+audio transceivers, POSTs the SDP offer, and applies the answer', async () => {
    let pc: FakePeerConnection | undefined
    vi.stubGlobal(
      'RTCPeerConnection',
      class extends FakePeerConnection {
        constructor() {
          super()
          pc = this
        }
      },
    )
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('fake-answer-sdp', { status: 200, headers: { Location: '/v3/whep/1/session' } }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const video = document.createElement('video')
    const controller = new AbortController()
    await connectWhep('http://mediamtx:8889/1/whep', video, controller.signal)

    expect(pc?.transceivers).toEqual([
      { kind: 'video', direction: 'recvonly' },
      { kind: 'audio', direction: 'recvonly' },
    ])
    expect(fetchMock).toHaveBeenCalledWith('http://mediamtx:8889/1/whep', {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: 'fake-offer-sdp',
    })
    expect(pc?.remoteDescription).toEqual({ type: 'answer', sdp: 'fake-answer-sdp' })
  })

  it('throws on a non-ok WHEP response', async () => {
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('nope', { status: 404, statusText: 'Not Found' })))

    const video = document.createElement('video')
    await expect(connectWhep('http://mediamtx:8889/1/whep', video, new AbortController().signal)).rejects.toThrow(
      /404/,
    )
  })

  it('resolves the Location header against the page origin when the WHEP url itself is relative (same-origin nginx proxy)', async () => {
    let pc: FakePeerConnection | undefined
    vi.stubGlobal(
      'RTCPeerConnection',
      class extends FakePeerConnection {
        constructor() {
          super()
          pc = this
        }
      },
    )
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('fake-answer-sdp', { status: 200, headers: { Location: '/api/whep/1/whep/session-abc' } }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const video = document.createElement('video')
    const controller = new AbortController()
    await connectWhep('/api/whep/1/whep', video, controller.signal)

    expect(fetchMock).toHaveBeenCalledWith('/api/whep/1/whep', expect.anything())

    controller.abort()
    await vi.waitFor(() => expect(pc?.closed).toBe(true))
    expect(fetchMock).toHaveBeenCalledWith(`${window.location.origin}/api/whep/1/whep/session-abc`, { method: 'DELETE' })
  })

  it('DELETEs the WHEP resource URL (from the Location header) and closes the connection on abort', async () => {
    let pc: FakePeerConnection | undefined
    vi.stubGlobal(
      'RTCPeerConnection',
      class extends FakePeerConnection {
        constructor() {
          super()
          pc = this
        }
      },
    )
    const fetchMock = vi.fn().mockResolvedValue(
      new Response('fake-answer-sdp', { status: 200, headers: { Location: '/v3/whep/1/session' } }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const video = document.createElement('video')
    const controller = new AbortController()
    await connectWhep('http://mediamtx:8889/1/whep', video, controller.signal)

    controller.abort()
    await vi.waitFor(() => expect(pc?.closed).toBe(true))
    expect(fetchMock).toHaveBeenCalledWith('http://mediamtx:8889/v3/whep/1/session', { method: 'DELETE' })
  })
})
