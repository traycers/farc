import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import StoragesIndexPage from './StoragesIndexPage'
import type { ChannelInfo, StorageInfo } from '../../api/farcd'

const storages: StorageInfo[] = [
  { id: 's1', name: 'Storage One', path: '/data/s1', geometry: { FblockSize: 1024, N: 1, MaxChannels: 8 } },
  { id: 's2', name: 'Storage Two', path: '/data/s2', geometry: { FblockSize: 1024, N: 1, MaxChannels: 4 } },
]

const channels: ChannelInfo[] = [
  { channel: 1, rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
  { channel: 2, rtsp_url: 'rtsp://b', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
]

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => storages),
  listChannels: vi.fn(async () => channels),
}))

function renderPage() {
  return render(
    <MemoryRouter>
      <StoragesIndexPage />
    </MemoryRouter>,
  )
}

describe('StoragesIndexPage channels column', () => {
  it('shows the current channel count per storage, to the left of max channels', async () => {
    renderPage()

    const row1 = (await screen.findByText('Storage One')).closest('tr')!
    const row2 = screen.getByText('Storage Two').closest('tr')!

    const cells1 = Array.from(row1.querySelectorAll('td')).map((td) => td.textContent)
    const cells2 = Array.from(row2.querySelectorAll('td')).map((td) => td.textContent)

    // channels count (2) immediately left of max channels (8) for s1
    expect(cells1).toContain('2')
    expect(cells1.indexOf('2')).toBe(cells1.indexOf('8') - 1)

    // storage with zero channels shows 0, not blank
    expect(cells2).toContain('0')
    expect(cells2.indexOf('0')).toBe(cells2.indexOf('4') - 1)
  })
})
