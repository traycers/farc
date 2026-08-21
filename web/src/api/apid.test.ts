import { afterEach, describe, expect, it, vi } from 'vitest'
import { createChannel, getCameraURL, getLiveURLs, removeChannel, updateChannel } from './apid'

const okBody = JSON.stringify({ farcd: 'ok', mediamtx: 'ok' })

describe('createChannel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('POSTs to /api/apid/channels with the given payload', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(okBody, { status: 201 }))
    vi.stubGlobal('fetch', fetchMock)

    const input = {
      id: 1,
      rtsp_url: 'rtsp://camera/1',
      storage: 's1',
      capture_policy: { type: 'continuous' as const },
      name: 'front door',
    }
    const result = await createChannel(input)

    expect(fetchMock).toHaveBeenCalledWith('/api/apid/channels', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    expect(result).toEqual({ farcd: 'ok', mediamtx: 'ok' })
  })

  it('throws on a 207 partial failure, surfacing which side failed', async () => {
    const body = JSON.stringify({ farcd: 'ok', mediamtx: 'error: connection refused' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 207 })))

    await expect(
      createChannel({ id: 1, rtsp_url: 'rtsp://camera/1', storage: 's1', capture_policy: { type: 'continuous' }, name: '' }),
    ).rejects.toThrow(/mediamtx.*connection refused/)
  })

  it('throws with the error body on a 400', async () => {
    const body = JSON.stringify({ error: 'apid: decode request body: EOF' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 400, statusText: 'Bad Request' })))

    await expect(
      createChannel({ id: 1, rtsp_url: '', storage: '', capture_policy: { type: 'continuous' }, name: '' }),
    ).rejects.toThrow(/400.*decode request body/)
  })
})

describe('updateChannel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('PATCHes to /api/apid/channels/{id}', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(okBody, { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const input = { rtsp_url: 'rtsp://camera/1', storage: 's1', capture_policy: { type: 'continuous' as const }, name: 'renamed' }
    await updateChannel(1, input)

    expect(fetchMock).toHaveBeenCalledWith('/api/apid/channels/1', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
  })
})

describe('getCameraURL', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('GETs /api/apid/channels/{id} and returns camera_rtsp_url', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ camera_rtsp_url: 'rtsp://camera/1' }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const url = await getCameraURL(1)

    expect(fetchMock).toHaveBeenCalledWith('/api/apid/channels/1')
    expect(url).toBe('rtsp://camera/1')
  })

  it('throws on a 404 (no mediamtx path configured)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'no mediamtx path configured' }), { status: 404 })),
    )

    await expect(getCameraURL(1)).rejects.toThrow(/404.*no mediamtx path configured/)
  })
})

describe('getLiveURLs', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('GETs /api/apid/channels/live-urls?ids=... once for the whole batch', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ urls: { '1': 'http://mediamtx:8889/1/whep', '2': 'http://mediamtx:8889/2/whep' } }), {
          status: 200,
        }),
      )
    vi.stubGlobal('fetch', fetchMock)

    const urls = await getLiveURLs([1, 2])

    expect(fetchMock).toHaveBeenCalledWith('/api/apid/channels/live-urls?ids=1,2')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(urls).toEqual({ 1: 'http://mediamtx:8889/1/whep', 2: 'http://mediamtx:8889/2/whep' })
  })

  it('returns an empty map without a request for an empty id list', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    const urls = await getLiveURLs([])

    expect(urls).toEqual({})
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('removeChannel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('DELETEs /api/apid/channels/{id}', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(okBody, { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await removeChannel(1)

    expect(fetchMock).toHaveBeenCalledWith('/api/apid/channels/1', { method: 'DELETE' })
  })
})
