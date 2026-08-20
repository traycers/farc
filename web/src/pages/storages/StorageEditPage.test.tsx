import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import StorageEditPage from './StorageEditPage'
import type { StorageInfo } from '../../api/farcd'

const storage: StorageInfo = {
  id: 's1',
  path: '/data/s1',
  name: 'Storage One',
  geometry: { FblockSize: 1_000_000, N: 4, MaxChannels: 8 },
}

const { patchStorage, deleteStorage } = vi.hoisted(() => ({
  patchStorage: vi.fn(async () => {}),
  deleteStorage: vi.fn(async () => {}),
}))

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => [storage]),
  patchStorage,
  deleteStorage,
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/s1/edit']}>
      <Routes>
        <Route path="/storages/:id/edit" element={<StorageEditPage />} />
        <Route path="/storages" element={<div data-testid="storages-index" />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('StorageEditPage detach button', () => {
  beforeEach(() => {
    patchStorage.mockClear()
    deleteStorage.mockClear()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('does not call deleteStorage when the confirm dialog is declined', async () => {
    vi.stubGlobal('confirm', vi.fn(() => false))
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /detach/i }))

    expect(deleteStorage).not.toHaveBeenCalled()
  })

  it('deletes and navigates to /storages when confirmed', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /detach/i }))

    expect(deleteStorage).toHaveBeenCalledWith('s1')
    await screen.findByTestId('storages-index')
  })

  it('shows the error and does not navigate on failure (e.g. 409 with attached channel)', async () => {
    deleteStorage.mockRejectedValueOnce(new Error('409 Conflict: api: storage "s1" still has channel 3 attached'))
    vi.stubGlobal('confirm', vi.fn(() => true))
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: /detach/i }))

    await screen.findByText(/still has channel 3 attached/)
    expect(screen.queryByTestId('storages-index')).toBeNull()
  })
})
