import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LivePage from './LivePage'
import type { ChannelInfo } from '../api/farcd'
import { getLiveURLs } from '../api/apid'

let channels: ChannelInfo[] = []

vi.mock('../api/farcd', () => ({
  listChannels: vi.fn(() => Promise.resolve(channels)),
}))

vi.mock('../api/apid', () => ({
  getLiveURLs: vi.fn(async (ids: number[]) => Object.fromEntries(ids.map((id) => [id, `http://mediamtx:8889/${id}/whep`]))),
}))

vi.mock('../components/LiveVideoTile', () => ({
  default: ({ channel, whepUrl, active, onClick }: { channel: number; whepUrl: string | null; active: boolean; onClick: () => void }) => (
    <div data-testid={`mock-live-tile-${channel}`} data-whep-url={whepUrl ?? ''} data-active={String(active)} onClick={onClick} />
  ),
}))

function renderPage() {
  return render(
    <MemoryRouter>
      <LivePage />
    </MemoryRouter>,
  )
}

describe('LivePage', () => {
  beforeEach(() => {
    localStorage.clear()
    channels = [
      { channel: 1, name: 'front door', rtsp_url: 'rtsp://mediamtx:8554/1', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: true },
      { channel: 2, name: 'back yard', rtsp_url: 'rtsp://mediamtx:8554/2', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: false, last_connect_error: 'timeout' },
    ]
  })

  it('lists channels with a status indicator, name, checkbox, and a link to the archive player', async () => {
    renderPage()

    await screen.findByText(/front door/)
    expect(screen.getByTestId('channel-status-outer-1')).toHaveClass('channel-status-connected')
    expect(screen.getByTestId('channel-recording-dot-1')).toHaveClass('status-dot-recording')
    expect(screen.getByTestId('channel-status-outer-2')).toHaveClass('channel-status-disconnected')

    const links = screen.getAllByRole('link', { name: /в архив/i })
    expect(links[0]).toHaveAttribute('href', '/player?channel=1')
    expect(links[1]).toHaveAttribute('href', '/player?channel=2')
  })

  it('checking a channel adds it to the live grid, fetching WHEP URLs in one batch call', async () => {
    renderPage()

    fireEvent.click(await screen.findByLabelText('show channel 1'))

    await screen.findByTestId('mock-live-tile-1')
    expect(getLiveURLs).toHaveBeenCalledWith([1])
    expect(screen.getByTestId('mock-live-tile-1')).toHaveAttribute('data-whep-url', 'http://mediamtx:8889/1/whep')
  })

  it('unchecking a channel removes its tile from the grid', async () => {
    renderPage()

    const checkbox = await screen.findByLabelText('show channel 1')
    fireEvent.click(checkbox)
    await screen.findByTestId('mock-live-tile-1')

    fireEvent.click(checkbox)
    expect(screen.queryByTestId('mock-live-tile-1')).not.toBeInTheDocument()
  })

  it('persists checked channels in localStorage across mounts', async () => {
    const { unmount } = renderPage()
    fireEvent.click(await screen.findByLabelText('show channel 1'))
    await screen.findByTestId('mock-live-tile-1')
    unmount()

    renderPage()
    expect(await screen.findByTestId('mock-live-tile-1')).toBeInTheDocument()
    expect(await screen.findByLabelText('show channel 1')).toBeChecked()
  })

  it('starts with nothing checked on a first-ever visit (no default-all)', async () => {
    renderPage()

    await screen.findByLabelText('show channel 1')
    expect(screen.getByLabelText('show channel 1')).not.toBeChecked()
    expect(screen.queryByTestId('mock-live-tile-1')).not.toBeInTheDocument()
  })

  it('clicking a tile makes it the active one, mirroring the Player audio model', async () => {
    renderPage()

    fireEvent.click(await screen.findByLabelText('show channel 1'))
    fireEvent.click(await screen.findByLabelText('show channel 2'))
    const tile1 = await screen.findByTestId('mock-live-tile-1')
    const tile2 = await screen.findByTestId('mock-live-tile-2')
    expect(tile1.dataset.active).toBe('false')
    expect(tile2.dataset.active).toBe('false')

    fireEvent.click(tile1)
    expect(tile1.dataset.active).toBe('true')
    expect(tile2.dataset.active).toBe('false')
  })
})
