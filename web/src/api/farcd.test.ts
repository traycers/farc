import { afterEach, describe, expect, it, vi } from 'vitest'
import { deleteStorage } from './farcd'

describe('deleteStorage', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('DELETEs the storage by id', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await deleteStorage('s1')

    expect(fetchMock).toHaveBeenCalledWith('/api/farcd/storages/s1', { method: 'DELETE' })
  })

  it('encodes the id in the URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await deleteStorage('s/1')

    expect(fetchMock).toHaveBeenCalledWith('/api/farcd/storages/s%2F1', { method: 'DELETE' })
  })

  it('throws with the response body on a non-ok status, e.g. 409 with attached channel', async () => {
    const body = 'api: storage "s1" still has channel 3 attached'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 409, statusText: 'Conflict' })))

    await expect(deleteStorage('s1')).rejects.toThrow(/409.*still has channel 3 attached/)
  })
})
