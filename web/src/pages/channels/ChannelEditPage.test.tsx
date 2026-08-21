import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import ChannelEditPage from './ChannelEditPage'
import type { ChannelInfo, StorageInfo } from '../../api/farcd'
import { getCameraURL, updateChannel } from '../../api/apid'

const storages: StorageInfo[] = [
  { id: 's1', name: 'Storage One', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 }, pool: { Size: 4, WarningAt: 2, BackpressureAt: 4 } },
  { id: 's2', name: 'Storage Two', path: '/data/s2', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 }, pool: { Size: 4, WarningAt: 2, BackpressureAt: 4 } },
]

// rtsp_url here is mediamtx's re-serve address (what farcd actually
// stores, .scratch/live-page/spec.md) -- deliberately NOT what the edit
// form should show; that comes from apid's getCameraURL mock below.
const channels: ChannelInfo[] = [
  { channel: 1, rtsp_url: 'rtsp://mediamtx:8554/1', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
  { channel: 2, rtsp_url: 'rtsp://mediamtx:8554/2', storage: 's2', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
]

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => storages),
  listChannels: vi.fn(async () => channels),
}))

vi.mock('../../api/apid', () => ({
  getCameraURL: vi.fn(async () => 'rtsp://camera/1'),
  updateChannel: vi.fn(async () => ({ farcd: 'ok', mediamtx: 'ok' })),
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

describe('ChannelEditPage rtsp_url prefill (.scratch/live-page)', () => {
  it("prefills rtsp_url from apid's getCameraURL, not farcd's stored (mediamtx) rtsp_url", async () => {
    renderPage()

    expect(await screen.findByDisplayValue('rtsp://camera/1')).toBeInTheDocument()
    expect(getCameraURL).toHaveBeenCalledWith(1)
  })

  it('submits via apid.updateChannel, not farcd directly', async () => {
    renderPage()

    await screen.findByDisplayValue('rtsp://camera/1')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await vi.waitFor(() => expect(updateChannel).toHaveBeenCalled())
    expect(vi.mocked(updateChannel).mock.calls[0]).toEqual([
      1,
      expect.objectContaining({ rtsp_url: 'rtsp://camera/1', storage: 's1' }),
    ])
  })
})

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
