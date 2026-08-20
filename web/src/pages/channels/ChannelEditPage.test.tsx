import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import ChannelEditPage from './ChannelEditPage'
import type { ChannelInfo, StorageInfo } from '../../api/farcd'

const storages: StorageInfo[] = [
  { id: 's1', name: 'Storage One', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 }, pool: { Size: 4, WarningAt: 2, BackpressureAt: 4 } },
  { id: 's2', name: 'Storage Two', path: '/data/s2', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 }, pool: { Size: 4, WarningAt: 2, BackpressureAt: 4 } },
]

const channels: ChannelInfo[] = [
  { channel: 1, rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
  { channel: 2, rtsp_url: 'rtsp://b', storage: 's2', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
]

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => storages),
  listChannels: vi.fn(async () => channels),
  updateChannel: vi.fn(async () => ({})),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/channels/1/edit']}>
      <Routes>
        <Route path="/channels/:id/edit" element={<ChannelEditPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('ChannelEditPage full-storage guard', () => {
  it("never disables the channel's own current storage, even though it's full", async () => {
    renderPage()

    const ownOption = (await screen.findByRole('option', { name: /storage one/i })) as HTMLOptionElement
    expect(ownOption.disabled).toBe(false)
    expect(ownOption.textContent).not.toContain('full')
    expect(screen.getByRole('button', { name: /save changes/i })).not.toBeDisabled()
  })

  it('disables a different, full storage in the select and blocks submit once selected', async () => {
    renderPage()

    const otherOption = (await screen.findByRole('option', { name: /storage two/i })) as HTMLOptionElement
    expect(otherOption.disabled).toBe(true)
    expect(otherOption.textContent).toContain('full, 1/1')

    fireEvent.change(screen.getByRole('combobox', { name: /storage/i }), { target: { value: 's2' } })
    expect(screen.getByRole('button', { name: /save changes/i })).toBeDisabled()
  })
})
