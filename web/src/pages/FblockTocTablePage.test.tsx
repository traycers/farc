import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import FblockTocTablePage from './FblockTocTablePage'
import type { TocRow } from '../api/fblockTree'
import { downloadTextFile } from '../lib/download'
import { tocRowsToCsv } from './fblockTocTablePaging'

vi.mock('../lib/download', () => ({ downloadTextFile: vi.fn() }))

// react-virtual's useVirtualizer measures the scroll container's real DOM
// size, which jsdom always reports as 0 -- stub it to always report every
// row as a virtual item, so the test exercises this page's own filter/CSV
// wiring rather than the third-party library's viewport math.
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: (opts: { count: number }) => ({
    getVirtualItems: () => Array.from({ length: opts.count }, (_, i) => ({ index: i, start: i * 28, size: 28, key: i })),
    getTotalSize: () => opts.count * 28,
  }),
}))

const rows: TocRow[] = [
  { id: 0, type: 'void', role: 'root', parent_id: 0, sibling_id: 0, value_or_offset: '0', size: 0 },
  { id: 1, type: 'uint32', role: 'channel', parent_id: 0, sibling_id: 1, value_or_offset: '5', size: 0 },
  { id: 2, type: 'bytes', role: 'frame_data(video)', parent_id: 0, sibling_id: 1, value_or_offset: '0', size: 42 },
]

vi.mock('../api/fblockTree', async () => {
  const actual = await vi.importActual<typeof import('../api/fblockTree')>('../api/fblockTree')
  return {
    ...actual,
    getFblockInfo: vi.fn(async () => ({ index: 0, state: 'ready', uuid: 'abc' })),
    getFblockTocRows: vi.fn(async () => rows),
  }
})

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/s1/fblocks/0/tree/toc']}>
      <Routes>
        <Route path="/storages/:id/fblocks/:index/tree/toc" element={<FblockTocTablePage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('FblockTocTablePage', () => {
  it('renders every fetched row', async () => {
    renderPage()
    const table = within(await screen.findByTestId('toc-rows'))
    expect(table.getByText('channel')).toBeInTheDocument()
    expect(table.getByText('frame_data(video)')).toBeInTheDocument()
    expect(table.getByText('root')).toBeInTheDocument()
  })

  it('hides non-matching rows when a role filter is applied', async () => {
    renderPage()
    const table = within(await screen.findByTestId('toc-rows'))
    table.getByText('channel')
    fireEvent.change(screen.getByLabelText(/role/i), { target: { value: 'channel' } })
    expect(table.getByText('channel')).toBeInTheDocument()
    expect(table.queryByText('root')).not.toBeInTheDocument()
    expect(table.queryByText('frame_data(video)')).not.toBeInTheDocument()
  })

  it('downloads the FULL, unfiltered row set as CSV even while a filter is active', async () => {
    renderPage()
    const table = within(await screen.findByTestId('toc-rows'))
    table.getByText('channel')
    fireEvent.change(screen.getByLabelText(/role/i), { target: { value: 'channel' } })
    expect(table.queryByText('root')).not.toBeInTheDocument() // filter really is active

    fireEvent.click(screen.getByRole('button', { name: /download toc/i }))

    expect(downloadTextFile).toHaveBeenCalledTimes(1)
    const [filename, content, mimeType] = vi.mocked(downloadTextFile).mock.calls[0]
    expect(filename).toBe('fblock-abc-toc.csv')
    expect(mimeType).toBe('text/csv')
    expect(content).toBe(tocRowsToCsv(rows)) // ALL rows, not just 'channel'
  })
})
