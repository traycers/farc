import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import ChannelNewPage from './ChannelNewPage'
import { listChannels, listStorages } from '../../api/farcd'

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => []),
  listChannels: vi.fn(async () => []),
  createChannel: vi.fn(async () => {}),
}))

function renderPage(initialEntries: string[] = ['/channels/new']) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <ChannelNewPage />
    </MemoryRouter>,
  )
}

describe('ChannelNewPage rtsp_url generate button', () => {
  it('fills rtsp_url with the local test mediamtx stream on click', async () => {
    renderPage()

    fireEvent.click(await screen.findByTestId('rtsp-url-generate-btn'))

    expect(screen.getByPlaceholderText('rtsp://camera1/stream')).toHaveValue('rtsp://mediamtx:8554/test')
  })
})

describe('ChannelNewPage default storage selection', () => {
  const twoStorages = [
    { id: 's1', name: 'Storage One', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } },
    { id: 's2', name: 'Storage Two', path: '/data/s2', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } },
  ]

  it('defaults to the storage named in the ?storage= query param', async () => {
    vi.mocked(listStorages).mockResolvedValueOnce(twoStorages)
    renderPage(['/channels/new?storage=s2'])

    expect(await screen.findByRole('combobox', { name: /storage/i })).toHaveValue('s2')
  })

  it('falls back to the first storage when the query param is missing or unknown', async () => {
    vi.mocked(listStorages).mockResolvedValueOnce(twoStorages)
    renderPage(['/channels/new?storage=unknown'])

    expect(await screen.findByRole('combobox', { name: /storage/i })).toHaveValue('s1')
  })
})

describe('ChannelNewPage full-storage guard', () => {
  const oneFullOneNot = [
    { id: 's1', name: 'Storage One', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 } },
    { id: 's2', name: 'Storage Two', path: '/data/s2', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } },
  ]
  const oneChannelOnS1 = [
    { channel: 1, rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous' as const, prerecord_ns: 0, postrecord_ns: 0 },
  ]

  it('disables the full storage option, labeled, and the submit button when it is selected', async () => {
    vi.mocked(listStorages).mockResolvedValueOnce(oneFullOneNot)
    vi.mocked(listChannels).mockResolvedValueOnce(oneChannelOnS1)
    renderPage(['/channels/new?storage=s1'])

    const option = (await screen.findByRole('option', { name: /storage one/i })) as HTMLOptionElement
    expect(option.disabled).toBe(true)
    expect(option.textContent).toContain('full, 1/1')
    expect(screen.getByRole('button', { name: /add channel/i })).toBeDisabled()
  })

  it('keeps the submit button enabled when a non-full storage is selected', async () => {
    vi.mocked(listStorages).mockResolvedValueOnce(oneFullOneNot)
    vi.mocked(listChannels).mockResolvedValueOnce(oneChannelOnS1)
    renderPage(['/channels/new?storage=s2'])

    await screen.findByRole('option', { name: /storage two/i })
    expect(screen.getByRole('button', { name: /add channel/i })).not.toBeDisabled()
  })
})
