import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import StorageNewPage from './StorageNewPage'

const { createStorage } = vi.hoisted(() => ({
  createStorage: vi.fn(async () => ({ id: 's1' })),
}))

vi.mock('../../api/farcd', () => ({
  createStorage,
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/new']}>
      <Routes>
        <Route path="/storages/new" element={<StorageNewPage />} />
        <Route path="/storages" element={<div data-testid="storages-index" />} />
      </Routes>
    </MemoryRouter>,
  )
}

function fillRequiredFields() {
  fireEvent.change(screen.getByLabelText(/^id$/i), { target: { value: 's1' } })
  fireEvent.change(screen.getByLabelText(/^path$/i), { target: { value: '/data/s1.img' } })
}

describe('StorageNewPage pool tuning fields', () => {
  beforeEach(() => {
    createStorage.mockClear()
  })

  it('submits the default pool tuning (4/2/4) when left untouched', async () => {
    renderPage()
    fillRequiredFields()

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await screen.findByTestId('storages-index')
    expect(createStorage).toHaveBeenCalledWith(
      expect.objectContaining({ pool: { Size: 4, WarningAt: 2, BackpressureAt: 4 } }),
    )
  })

  it('submits edited pool tuning values', async () => {
    renderPage()
    fillRequiredFields()

    fireEvent.change(screen.getByLabelText(/pool size/i), { target: { value: '8' } })
    fireEvent.change(screen.getByLabelText(/pool warning at/i), { target: { value: '4' } })
    fireEvent.change(screen.getByLabelText(/pool backpressure at/i), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await screen.findByTestId('storages-index')
    expect(createStorage).toHaveBeenCalledWith(
      expect.objectContaining({ pool: { Size: 8, WarningAt: 4, BackpressureAt: 8 } }),
    )
  })

  it('shows the RAM estimate helper for pool size', () => {
    renderPage()
    // default fblockSizeMiB = 1024 (1 GiB), default poolSize = 4 -> 4 GiB
    expect(screen.getByText(/4\.00 GiB RAM/i)).toBeInTheDocument()
  })

  it('shows the warning/backpressure percentage helpers', () => {
    renderPage()
    // defaults: poolWarningAt=2, poolBackpressureAt=4, poolSize=4
    expect(screen.getByText(/2 of 4 slots \(50%\)/i)).toBeInTheDocument()
    expect(screen.getByText(/4 of 4 slots \(100%\)/i)).toBeInTheDocument()
  })

  it('does not crash or show NaN%/Infinity% when pool size is cleared to 0', () => {
    renderPage()
    fireEvent.change(screen.getByLabelText(/pool size/i), { target: { value: '0' } })

    expect(screen.queryByText(/NaN%/i)).toBeNull()
    expect(screen.queryByText(/Infinity%/i)).toBeNull()
    expect(screen.getAllByText(/—/).length).toBeGreaterThan(0)
  })
})

describe('StorageNewPage min container share field', () => {
  beforeEach(() => {
    createStorage.mockClear()
  })

  // Matches fblock.DefaultMinContainerShare (internal/storage's own
  // backend default) -- the UI default must not silently diverge from it.
  it('submits the backend default of 0.7 (shown as 70%) when left untouched', async () => {
    renderPage()
    fillRequiredFields()

    expect(screen.getByLabelText(/min container share/i)).toHaveValue(70)

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await screen.findByTestId('storages-index')
    expect(createStorage).toHaveBeenCalledWith(
      expect.objectContaining({ params: expect.objectContaining({ min_container_share: 0.7 }) }),
    )
  })

  it('shows the resulting absolute size helper for the default 70%/1 GiB fblock', () => {
    renderPage()
    // 70% of a 1024 MiB (1 GiB) fblock = 716.8 MiB / 1.00 GiB
    expect(screen.getByText(/716\.80 MiB \/ 1\.00 GiB/i)).toBeInTheDocument()
  })

  it('updates the absolute size helper when the percentage changes', () => {
    renderPage()
    fireEvent.change(screen.getByLabelText(/min container share/i), { target: { value: '50' } })

    expect(screen.getByText(/512\.00 MiB \/ 1\.00 GiB/i)).toBeInTheDocument()
  })

  it('converts an edited percentage back to a fraction on submit', async () => {
    renderPage()
    fillRequiredFields()

    fireEvent.change(screen.getByLabelText(/min container share/i), { target: { value: '50' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await screen.findByTestId('storages-index')
    expect(createStorage).toHaveBeenCalledWith(
      expect.objectContaining({ params: expect.objectContaining({ min_container_share: 0.5 }) }),
    )
  })
})
