import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelsIndexPage from './ChannelsIndexPage'
import type { ChannelInfo, StorageInfo } from '../../api/farcd'
import type { JournalEvent } from '../../api/events'

let storages: StorageInfo[] = [{ id: 's1', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } }]
let channels: ChannelInfo[] = []

vi.mock('../../api/farcd', () => ({
  listStorages: vi.fn(async () => storages),
  listChannels: vi.fn(async () => channels),
  removeChannel: vi.fn(),
  startRecording: vi.fn(),
  stopRecording: vi.fn(),
  triggerEvent: vi.fn(),
}))

let onJournalEvent: (e: JournalEvent) => void = () => {}
const subscribeJournal = vi.fn((onEvent: (e: JournalEvent) => void) => {
  onJournalEvent = onEvent
  return () => {}
})
vi.mock('../../api/events', () => ({
  subscribeJournal: (onEvent: (e: JournalEvent) => void) => subscribeJournal(onEvent),
}))

beforeEach(() => {
  onJournalEvent = () => {}
  storages = [{ id: 's1', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } }]
  channels = []
})

function renderPage() {
  return render(
    <MemoryRouter>
      <ChannelsIndexPage />
    </MemoryRouter>,
  )
}

describe('ChannelsIndexPage', () => {
  it('shows a red dot for a recording channel and a gray dot for an idle one', async () => {
    channels = [
      { channel: 1, name: 'cam1', rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: true },
      { channel: 2, name: 'cam2', rtsp_url: 'rtsp://b', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: false },
    ]
    renderPage()

    const recordingDot = await screen.findByTestId('channel-recording-dot-1')
    const idleDot = await screen.findByTestId('channel-recording-dot-2')
    expect(recordingDot.className).toContain('status-dot-recording')
    expect(idleDot.className).toContain('status-dot-idle')
  })

  it('shows a persistent per-channel error when last_connect_error is set', async () => {
    channels = [
      {
        channel: 1,
        name: 'cam1',
        rtsp_url: 'rtsp://a',
        storage: 's1',
        capture_policy_type: 'continuous',
        prerecord_ns: 0,
        postrecord_ns: 0,
        recording: false,
        last_connect_error: 'connection refused',
      },
    ]
    renderPage()

    expect(await screen.findByText('connection refused')).toBeInTheDocument()
  })

  it('flips the recording dot live on channel.recording.started/stopped without a refetch', async () => {
    channels = [
      { channel: 1, name: 'cam1', rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: false },
    ]
    renderPage()
    const dot = await screen.findByTestId('channel-recording-dot-1')
    expect(dot.className).toContain('status-dot-idle')

    act(() => onJournalEvent({ type: 'event', name: 'channel.recording.started', channel: 1 }))
    expect(screen.getByTestId('channel-recording-dot-1').className).toContain('status-dot-recording')

    act(() => onJournalEvent({ type: 'event', name: 'channel.recording.stopped', channel: 1 }))
    expect(screen.getByTestId('channel-recording-dot-1').className).toContain('status-dot-idle')
  })

  it('shows a banner and the row error live on channel.rtsp.connect_failed, clears on channel.rtsp.connected', async () => {
    channels = [
      { channel: 1, name: 'cam1', rtsp_url: 'rtsp://bad', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0, recording: false },
    ]
    renderPage()
    await screen.findByTestId('channel-recording-dot-1')

    act(() => onJournalEvent({ type: 'event', name: 'channel.rtsp.connect_failed', channel: 1, reason: 'connection refused' }))
    expect(screen.getByText('Channel 1: connection refused')).toBeInTheDocument()
    expect(screen.getByTestId('channel-connect-error-1')).toHaveTextContent('connection refused')

    act(() => onJournalEvent({ type: 'event', name: 'channel.rtsp.connected', channel: 1 }))
    expect(screen.queryByTestId('channel-connect-error-1')).not.toBeInTheDocument()
  })
})

describe('ChannelsIndexPage "New channel" capacity guard', () => {
  it('disables New channel with an explanatory title when the filtered storage is full', async () => {
    storages = [{ id: 's1', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 1 } }]
    channels = [
      { channel: 1, name: 'cam1', rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
    ]
    renderPage()

    const btn = await screen.findByRole('button', { name: /new channel/i })
    expect(btn).toBeDisabled()
    expect(btn.title).toContain('1/1')
  })

  it('keeps New channel as a working link to the storage-scoped form when there is room', async () => {
    storages = [{ id: 's1', path: '/data/s1', geometry: { FblockSize: 1, N: 1, MaxChannels: 8 } }]
    channels = []
    renderPage()

    const link = await screen.findByRole('link', { name: /new channel/i })
    expect(link).toHaveAttribute('href', '/new?storage=s1')
  })
})
