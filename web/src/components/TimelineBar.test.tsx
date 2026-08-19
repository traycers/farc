import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import TimelineBar from './TimelineBar'
import type { ChannelTimeline } from '../api/hls'

const timelines: ChannelTimeline[] = [
  { channel: 1, segments: [{ begin: 0n, end: 250n }] },
  { channel: 2, segments: [{ begin: 500n, end: 1000n }] },
]

describe('TimelineBar', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders one row per channel, with segment bars positioned by left/width percentages', () => {
    render(<TimelineBar timelines={timelines} rangeStart={0n} rangeEnd={1000n} playheadNs={0n} onSeek={() => {}} />)

    const rows = screen.getAllByTestId('player-timeline-row')
    expect(rows).toHaveLength(2)
    const seg1 = rows[0].querySelector('.player-timeline-segment') as HTMLElement
    expect(seg1.style.left).toBe('0%')
    expect(seg1.style.width).toBe('25%')
    const seg2 = rows[1].querySelector('.player-timeline-segment') as HTMLElement
    expect(seg2.style.left).toBe('50%')
    expect(seg2.style.width).toBe('50%')
  })

  // Regression-shaped per .scratch/fblocks-ui/issues/
  // 11-pool-bar-nested-percentage-collapses-to-zero.md: segments/cursor must
  // be direct position:absolute children of the position:relative row/bar,
  // no intermediate wrapper -- assert the DOM shape itself, not just the
  // style strings (which stay correct even when the layout is broken).
  it('positions the cursor as a direct child of the timeline container, no intermediate wrapper', () => {
    render(<TimelineBar timelines={timelines} rangeStart={0n} rangeEnd={1000n} playheadNs={300n} onSeek={() => {}} />)
    const bar = screen.getByTestId('player-timeline')
    const cursor = bar.querySelector('.player-timeline-cursor') as HTMLElement
    expect(cursor).not.toBeNull()
    expect(cursor.parentElement).toBe(bar)
    expect(cursor.style.left).toBe('30%')
  })

  it('clicking the bar seeks to the clicked fraction of the visible range', () => {
    const onSeek = vi.fn()
    render(<TimelineBar timelines={timelines} rangeStart={0n} rangeEnd={1000n} playheadNs={0n} onSeek={onSeek} />)
    const bar = screen.getByTestId('player-timeline')
    vi.spyOn(bar, 'getBoundingClientRect').mockReturnValue({ left: 0, width: 200 } as DOMRect)

    fireEvent.click(bar, { clientX: 100 })

    expect(onSeek).toHaveBeenCalledWith(500n)
  })

  it('renders a tick axis row below the timeline, using the measured width', () => {
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({ width: 800, left: 0 } as DOMRect)
    render(<TimelineBar timelines={timelines} rangeStart={0n} rangeEnd={3_600_000_000_000n} playheadNs={0n} onSeek={() => {}} />)

    const axis = screen.getByTestId('player-timeline-axis')
    const ticks = axis.querySelectorAll('.player-timeline-tick')
    expect(ticks.length).toBeGreaterThan(1)
    // No intermediate wrapper -- same DOM-shape lesson as the cursor test.
    expect(ticks[0].parentElement).toBe(axis)
  })
})
