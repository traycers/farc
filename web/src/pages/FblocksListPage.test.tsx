import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import FblocksListPage from './FblocksListPage'

vi.mock('../api/farcd', () => ({
  listStorages: vi.fn(async () => []),
}))

const entries = [
  { index: 0, state: 'ready' },
  { index: 1, state: 'uninitialized' },
  { index: 2, state: 'in_progress' },
]
vi.mock('../api/fblockTree', () => ({
  getCatalog: vi.fn(async () => entries),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/s1/fblocks-list']}>
      <Routes>
        <Route path="/storages/:id/fblocks-list" element={<FblocksListPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('FblocksListPage', () => {
  it('lists every fblock with a link to its tree page', async () => {
    renderPage()
    const link = await screen.findByRole('link', { name: /#0/i })
    expect(link.getAttribute('href')).toBe('/storages/s1/fblocks/0/tree')
    expect(await screen.findByText(/#2/)).toBeInTheDocument()
  })

  it('hides uninitialized entries by default, reveals them when the filter is unchecked', async () => {
    renderPage()
    await screen.findByRole('link', { name: /#0/i })
    expect(screen.queryByText(/#1/)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('checkbox', { name: /hide uninitialized/i }))
    expect(screen.getByText(/#1/)).toBeInTheDocument()
  })
})
