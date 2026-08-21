import { act, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import FblocksGridPage from './FblocksGridPage'
import type { PoolPushMessage } from '../api/pool'
import { getCatalog } from '../api/fblockTree'

let capturedOnPool: ((msg: PoolPushMessage) => void) | undefined
let capturedOnStatus: ((connected: boolean) => void) | undefined
let catalogResponses: { index: number; state: string }[][] = []

vi.mock('../api/farcd', () => ({
  listStorages: vi.fn(async () => [{ id: 's1', path: '/x', geometry: { FblockSize: 1000, N: 4, MaxChannels: 8 } }]),
}))
vi.mock('../api/events', () => ({
  subscribeStorageEvents: vi.fn((_id, _want, _onEvent, onStatus, poolOptions) => {
    capturedOnStatus = onStatus
    capturedOnPool = poolOptions?.onPool
    return () => {}
  }),
}))
vi.mock('../api/fblockTree', () => ({
  getCatalog: vi.fn(async () => catalogResponses.shift() ?? []),
  getFblockInfo: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(getCatalog).mockClear()
  capturedOnStatus = undefined
  catalogResponses = [
    [
      { index: 0, state: 'ready' },
      { index: 1, state: 'in_progress' },
      { index: 2, state: 'bad' },
      { index: 3, state: 'uninitialized' },
    ],
  ]
})

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/s1/fblocks']}>
      <Routes>
        <Route path="/storages/:id/fblocks" element={<FblocksGridPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('FblocksGridPage', () => {
  it('links ready and in_progress squares to the fblock-tree page, leaves bad/uninitialized non-interactive', async () => {
    renderPage()

    const readyLink = await screen.findByTitle('#0 ready')
    expect(readyLink.tagName).toBe('A')
    expect(readyLink.getAttribute('href')).toBe('/storages/s1/fblocks/0/tree')

    const inProgressLink = await screen.findByTitle('#1 in_progress')
    expect(inProgressLink.tagName).toBe('A')
    expect(inProgressLink.getAttribute('href')).toBe('/storages/s1/fblocks/1/tree')

    const badSquare = await screen.findByTitle('#2 bad')
    expect(badSquare.tagName).toBe('DIV')

    const uninitSquare = await screen.findByTitle('#3 uninitialized')
    expect(uninitSquare.tagName).toBe('DIV')
  })

  it('re-fetches the catalog once the WS subscription is confirmed connected, closing the subscribe-handshake race', async () => {
    catalogResponses = [[{ index: 0, state: 'in_progress' }], [{ index: 0, state: 'ready' }]]
    renderPage()

    await screen.findByTitle('#0 in_progress')
    expect(getCatalog).toHaveBeenCalledTimes(1)

    act(() => capturedOnStatus?.(true))

    await screen.findByTitle('#0 ready')
    expect(getCatalog).toHaveBeenCalledTimes(2)
  })

  it('renders a PoolStatusList row for each slot in a "pool" push, scaled to the selected storage\'s FblockSize', async () => {
    renderPage()
    await screen.findAllByRole('option') // wait for listStorages to resolve

    capturedOnPool?.({
      type: 'pool',
      storage: 's1',
      slots: [
        { state: 'active', index: 0, has_index: true, prolog_size: 100, catalog_size: 0, content_size: 0, toc_size: 0, epilog_size: 0 },
        { state: 'free', prolog_size: 0, catalog_size: 0, content_size: 0, toc_size: 0, epilog_size: 0 },
      ],
    })

    const rows = await screen.findAllByTestId('pool-status-row')
    expect(rows).toHaveLength(2)
    // 100/1000 (the mocked storage's FblockSize) -- confirms fblockSize was
    // actually threaded through from listStorages, not left at some default.
    expect((rows[0].querySelector('.pool-section-prolog') as HTMLElement).style.width).toBe('10%')
  })
})
