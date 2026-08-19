import { afterEach, describe, expect, it, vi } from 'vitest'
import { getTimeline, playlistUrl } from './hls'

describe('getTimeline', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetches the batch endpoint and parses begin/end as bigint', async () => {
    const text =
      '[{"channel":1,"segments":[{"begin":1800000000000000000,"end":1800000001000000000}]},' +
      '{"channel":2,"segments":[]}]'
    const fetchMock = vi.fn().mockResolvedValue(new Response(text, { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    const got = await getTimeline([1, 2], 1_800_000_000_000_000_000n, 1_800_000_002_000_000_000n)

    expect(got).toEqual([
      { channel: 1, segments: [{ begin: 1800000000000000000n, end: 1800000001000000000n }] },
      { channel: 2, segments: [] },
    ])
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/hls/timeline?channels=1,2&t1=1800000000000000000&t2=1800000002000000000',
    )
  })

  it('throws with the response body on a non-ok status', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('boom', { status: 500, statusText: 'oops' })))
    await expect(getTimeline([1], 0n, 1n)).rejects.toThrow(/500.*boom/)
  })
})

describe('playlistUrl', () => {
  it('builds the channel+range playlist URL', () => {
    expect(playlistUrl(3, 100n, 200n)).toBe('/api/hls/channels/3/hls/100/200/playlist.m3u8')
  })
})
