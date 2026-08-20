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
  // Deliberately not the UI's own default (4/2/4) -- distinguishes a real
  // prefill from listStorages() from a hardcoded default the component
  // might fall back to.
  pool: { Size: 8, WarningAt: 4, BackpressureAt: 8 },
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

describe('StorageEditPage pool tuning fields', () => {
  beforeEach(() => {
    patchStorage.mockClear()
  })

  it('pre-fills the three fields from the fetched storage.pool, not a UI default', async () => {
    renderPage()

    expect(await screen.findByLabelText(/pool size/i)).toHaveValue(8)
    expect(screen.getByLabelText(/pool warning at/i)).toHaveValue(4)
    expect(screen.getByLabelText(/pool backpressure at/i)).toHaveValue(8)
  })

  it('shows the percentage/RAM helper text for the fetched values', async () => {
    renderPage()

    await screen.findByLabelText(/pool size/i)
    // fblockSize = 1_000_000 bytes, poolSize = 8 -> ~0.01 GiB (well under 1 GiB)
    expect(screen.getByText(/GiB RAM when the pool is full/i)).toBeInTheDocument()
    expect(screen.getByText(/4 of 8 slots \(50%\)/i)).toBeInTheDocument()
    expect(screen.getByText(/8 of 8 slots \(100%\)/i)).toBeInTheDocument()
  })

  it('saves all three fields together under one pool key with a single button', async () => {
    renderPage()

    await screen.findByLabelText(/pool size/i)
    fireEvent.change(screen.getByLabelText(/pool size/i), { target: { value: '16' } })
    fireEvent.change(screen.getByLabelText(/pool warning at/i), { target: { value: '8' } })
    fireEvent.change(screen.getByLabelText(/pool backpressure at/i), { target: { value: '16' } })
    fireEvent.click(screen.getByRole('button', { name: /save pool/i }))

    await screen.findByText(/takes effect after farcd restart/i)
    expect(patchStorage).toHaveBeenCalledTimes(1)
    expect(patchStorage).toHaveBeenCalledWith('s1', { pool: { Size: 16, WarningAt: 8, BackpressureAt: 16 } })
  })
})
