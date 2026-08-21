import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LivePage from './LivePage'
import type { ChannelInfo } from '../api/farcd'
import type { JournalEvent } from '../api/events'
import { getLiveURLs } from '../api/apid'

let channels: ChannelInfo[] = []

vi.mock('../api/farcd', () => ({
  listChannels: vi.fn(() => Promise.resolve(channels)),
}))

vi.mock('../api/apid', () => ({
  getLiveURLs: vi.fn(async (ids: number[]) => Object.fromEntries(ids.map((id) => [id, `http://mediamtx:8889/${id}/whep`]))),
}))

let onJournalEvent: (e: JournalEvent) => void = () => {}
const subscribeJournal = vi.fn((onEvent: (e: JournalEvent) => void) => {
  onJournalEvent = onEvent
  return () => {}
})
vi.mock('../api/events', () => ({
  subscribeJournal: (onEvent: (e: JournalEvent) => void) => subscribeJournal(onEvent),
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
    onJournalEvent = () => {}
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

    const links = screen.getAllByRole('link', { name: /Открыть в архиве/i })
    expect(links[0]).toHaveAttribute('href', '/player?channel=1')
    expect(links[1]).toHaveAttribute('href', '/player?channel=2')
  })

  it('puts the archive icon-link first in the row and the variable-width name last', async () => {
    renderPage()

    const name = await screen.findByText(/front door/)
    const row = name.closest('li')
    expect(row).not.toBeNull()
    const children = Array.from(row!.children)
    expect(children[0].tagName).toBe('A')
    expect(children[0]).toHaveAttribute('href', '/player?channel=1')
    expect(children[0].querySelector('.bi-clock-history')).not.toBeNull()
    expect(children[1].tagName).toBe('INPUT')
    expect(children[2]).toHaveAttribute('data-testid', 'channel-status-outer-1')
    expect(children[3]).toBe(name)
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

  it('prunes checked ids for channels that no longer exist (deleted elsewhere), sizing the grid to the remaining ones', async () => {
    localStorage.setItem('farc.live-page.checked-channels', JSON.stringify([1, 2, 3]))
    channels = [channels[0], channels[1]] // channel 3 was removed

    renderPage()

    await screen.findByTestId('mock-live-tile-1')
    await screen.findByTestId('mock-live-tile-2')
    expect(screen.queryByTestId('mock-live-tile-3')).not.toBeInTheDocument()
    expect(screen.queryByTestId('player-video-grid-empty-cell')).not.toBeInTheDocument()
    expect(JSON.parse(localStorage.getItem('farc.live-page.checked-channels')!)).toEqual([1, 2])
  })

  it('updates the recording dot live on channel.recording.started/stopped without a refetch', async () => {
    renderPage()
    const dot = await screen.findByTestId('channel-recording-dot-2')
    expect(dot.className).toContain('status-dot-idle')

    act(() => onJournalEvent({ type: 'event', name: 'channel.recording.started', channel: 2 }))
    expect(screen.getByTestId('channel-recording-dot-2').className).toContain('status-dot-recording')

    act(() => onJournalEvent({ type: 'event', name: 'channel.recording.stopped', channel: 2 }))
    expect(screen.getByTestId('channel-recording-dot-2').className).toContain('status-dot-idle')
  })

  it('updates the outer status ring live on channel.rtsp.connect_failed/connected without a refetch', async () => {
    renderPage()
    const outer = await screen.findByTestId('channel-status-outer-1')
    expect(outer.className).toContain('channel-status-connected')

    act(() => onJournalEvent({ type: 'event', name: 'channel.rtsp.connect_failed', channel: 1, reason: 'timeout' }))
    expect(screen.getByTestId('channel-status-outer-1').className).toContain('channel-status-disconnected')

    act(() => onJournalEvent({ type: 'event', name: 'channel.rtsp.connected', channel: 1 }))
    expect(screen.getByTestId('channel-status-outer-1').className).toContain('channel-status-connected')
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
