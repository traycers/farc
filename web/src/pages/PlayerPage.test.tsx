import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PlayerPage from './PlayerPage'
import type { ChannelInfo } from '../api/farcd'
import type { ChannelTimeline } from '../api/hls'
import { nsToLocalInputValue } from '../api/ns'

const channels: ChannelInfo[] = [
  { channel: 1, rtsp_url: 'rtsp://a', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
  { channel: 2, rtsp_url: 'rtsp://b', storage: 's1', capture_policy_type: 'continuous', prerecord_ns: 0, postrecord_ns: 0 },
]

let timelineFixture: ChannelTimeline[] = []

vi.mock('../api/farcd', () => ({
  listChannels: vi.fn(() => Promise.resolve(channels)),
}))

vi.mock('../api/hls', () => ({
  getTimeline: vi.fn(() => Promise.resolve(timelineFixture)),
  playlistUrl: (channel: number, t1: bigint, t2: bigint) => `/api/hls/channels/${channel}/hls/${t1}/${t2}/playlist.m3u8`,
}))

vi.mock('../components/VideoTile', () => ({
  default: ({
    channel,
    segmentUrl,
    active,
    onClick,
  }: {
    channel: number
    segmentUrl: string | null
    active: boolean
    onClick: () => void
  }) => (
    <div
      data-testid={`mock-tile-${channel}`}
      data-segment-url={segmentUrl ?? ''}
      data-active={String(active)}
      onClick={onClick}
    />
  ),
}))

describe('PlayerPage', () => {
  beforeEach(() => {
    timelineFixture = []
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('checklist -> search -> grid renders in the shape matching the checked channel count', async () => {
    timelineFixture = [
      { channel: 1, segments: [{ begin: 0n, end: 1_000_000_000n }] },
      { channel: 2, segments: [{ begin: 0n, end: 1_000_000_000n }] },
    ]
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    fireEvent.click(screen.getByLabelText('channel 1'))
    fireEvent.click(screen.getByLabelText('channel 2'))
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))

    await screen.findByTestId('mock-tile-1')
    const grid = screen.getByTestId('player-video-grid')
    expect(grid.style.gridTemplateColumns).toBe('repeat(2, 1fr)')
    expect(screen.getByTestId('mock-tile-2')).toBeInTheDocument()
  })

  it('clicking a tile makes it the active/audible one', async () => {
    timelineFixture = [{ channel: 1, segments: [{ begin: 0n, end: 1_000_000_000n }] }]
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    fireEvent.click(screen.getByLabelText('channel 1'))
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))

    const tile = await screen.findByTestId('mock-tile-1')
    expect(tile.dataset.active).toBe('false')
    fireEvent.click(tile)
    expect(tile.dataset.active).toBe('true')
  })

  it('gap-skip wiring: play advances the playhead and jumps a channel-wide gap automatically', async () => {
    // Both segments belong to channel 1: [0,50ms) then a gap, then
    // [300ms,400ms). One 200ms tick lands inside the gap -> must skip to
    // the second segment's begin, not just sit gapped.
    timelineFixture = [
      {
        channel: 1,
        segments: [
          { begin: 0n, end: 50_000_000n },
          { begin: 300_000_000n, end: 400_000_000n },
        ],
      },
    ]
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    fireEvent.click(screen.getByLabelText('channel 1'))
    // Pin the searched range to start exactly at unix-ns 0, via the same
    // nsToLocalInputValue the page itself uses -- a literal
    // '1970-01-01T00:00' string would be parsed in the test runner's local
    // timezone by nsFromLocalInputValue, landing on a nonzero (possibly
    // negative) ns value outside this fixture's small ns-scale segments.
    fireEvent.change(screen.getByLabelText('from'), { target: { value: nsToLocalInputValue(0n) } })
    fireEvent.change(screen.getByLabelText('to'), { target: { value: nsToLocalInputValue(600_000_000_000n) } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await screen.findByTestId('mock-tile-1')

    // Fake timers only from here: the setup above involves real async
    // module-mock resolution that doesn't need (and hangs under) faked time.
    vi.useFakeTimers()
    fireEvent.click(screen.getByRole('button', { name: 'Play' }))
    // Flush the effect that creates the interval (scheduled as a passive
    // effect, which under fake timers doesn't settle synchronously with
    // fireEvent) before counting the first real 200ms tick.
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(200)
    // React 19 commits a state update made from outside its own event
    // system (this interval callback) one microtask turn later than the
    // timer that triggered it -- confirmed by instrumenting the interval
    // callback directly: the very first tick already computes the correct
    // { kind: 'skip', to: 300_000_000n } step, but that commit isn't yet
    // painted into the DOM by the time the assertion below would otherwise
    // run. A zero-length advance flushes it without waiting for (or
    // depending on) a second real tick.
    await vi.advanceTimersByTimeAsync(0)

    const tile = screen.getByTestId('mock-tile-1')
    expect(tile.dataset.segmentUrl).toBe('/api/hls/channels/1/hls/300000000/400000000/playlist.m3u8')
  })

  it('Search button is disabled until at least one channel is checked', async () => {
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    expect(screen.getByRole('button', { name: 'Search' })).toBeDisabled()

    fireEvent.click(screen.getByLabelText('channel 1'))
    expect(screen.getByRole('button', { name: 'Search' })).not.toBeDisabled()

    fireEvent.click(screen.getByLabelText('channel 1'))
    expect(screen.getByRole('button', { name: 'Search' })).toBeDisabled()
  })

  it('search with zero matching segments shows a message and leaves the playhead unset', async () => {
    timelineFixture = [{ channel: 1, segments: [] }]
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    fireEvent.click(screen.getByLabelText('channel 1'))
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))

    await screen.findByText('No records found in the selected range')
    expect(screen.queryByTestId('player-current-time')).not.toBeInTheDocument()
  })

  it('search clips the playhead/range to the actual recordings, not the raw search window', async () => {
    // Search window is [0, 600s], but the only real recording is a narrow
    // slice starting at 5.3s -- rangeStart/playheadNs must land on the
    // data (floored to the whole second: 5s), not on the search window's t1.
    timelineFixture = [{ channel: 1, segments: [{ begin: 5_300_000_000n, end: 5_800_000_000n }] }]
    render(<PlayerPage />)

    await screen.findByLabelText('channel 1')
    fireEvent.click(screen.getByLabelText('channel 1'))
    fireEvent.change(screen.getByLabelText('from'), { target: { value: nsToLocalInputValue(0n) } })
    fireEvent.change(screen.getByLabelText('to'), { target: { value: nsToLocalInputValue(600_000_000_000n) } })
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await screen.findByTestId('mock-tile-1')

    expect(screen.getByTestId('player-current-time').getAttribute('data-playhead-ns')).toBe('5000000000')
  })
})
