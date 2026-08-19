import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import FblocksGridPage from './FblocksGridPage'
import type { PoolPushMessage } from '../api/pool'

let capturedOnPool: ((msg: PoolPushMessage) => void) | undefined

vi.mock('../api/farcd', () => ({
  listStorages: vi.fn(async () => [{ id: 's1', path: '/x', geometry: { FblockSize: 1000, N: 4, MaxChannels: 8 } }]),
}))
vi.mock('../api/events', () => ({
  subscribeStorageEvents: vi.fn((_id, _want, _onEvent, _onStatus, poolOptions) => {
    capturedOnPool = poolOptions?.onPool
    return () => {}
  }),
}))
vi.mock('../api/fblockTree', () => ({
  getCatalog: vi.fn(async () => [
    { index: 0, state: 'ready' },
    { index: 1, state: 'in_progress' },
    { index: 2, state: 'bad' },
    { index: 3, state: 'uninitialized' },
  ]),
  getFblockInfo: vi.fn(),
}))

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
