import { useEffect, useRef, useState, type MouseEvent } from 'react'
import type { ChannelTimeline } from '../api/hls'
import { fractionToNs, segmentToRect } from '../pages/timelineGeometry'
import { computeTicks } from '../pages/timelineTicks'

type TimelineBarProps = {
  timelines: ChannelTimeline[]
  rangeStart: bigint
  rangeEnd: bigint
  playheadNs: bigint
  onSeek: (ns: bigint) => void
}

// TimelineBar renders one row per channel plus a single shared cursor
// (.scratch/player-redesign/spec.md: one playhead for every visible
// channel, not one per row). Segments/cursor are direct position:absolute
// children of a position:relative container -- NOT nested inside an
// intermediate wrapper, per .scratch/fblocks-ui/issues/
// 11-pool-bar-nested-percentage-collapses-to-zero.md.
export default function TimelineBar({ timelines, rangeStart, rangeEnd, playheadNs, onSeek }: TimelineBarProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [axisWidth, setAxisWidth] = useState(0)

  // Measured once per data/range change, not on window resize -- the page
  // isn't expected to be resized mid-session (.scratch/player-redesign/
  // issues/03-timeline-axis-and-data-clipped-range.md).
  useEffect(() => {
    if (containerRef.current) setAxisWidth(containerRef.current.getBoundingClientRect().width)
  }, [timelines, rangeStart, rangeEnd])

  function handleClick(e: MouseEvent<HTMLDivElement>) {
    const rect = e.currentTarget.getBoundingClientRect()
    const fraction = (e.clientX - rect.left) / rect.width
    onSeek(fractionToNs(rangeStart, rangeEnd, fraction))
  }

  const cursorPct = segmentToRect({ begin: playheadNs, end: playheadNs }, rangeStart, rangeEnd).leftPct
  const ticks = computeTicks(rangeStart, rangeEnd, axisWidth)

  return (
    <>
      <div className="player-timeline" data-testid="player-timeline" onClick={handleClick} ref={containerRef}>
        {timelines.map((ct) => (
          <div className="player-timeline-row" data-testid="player-timeline-row" key={ct.channel}>
            {ct.segments.map((seg, i) => {
              const { leftPct, widthPct } = segmentToRect(seg, rangeStart, rangeEnd)
              return (
                <span
                  key={i}
                  className="player-timeline-segment"
                  style={{ left: `${leftPct}%`, width: `${widthPct}%` }}
                />
              )
            })}
          </div>
        ))}
        <span className="player-timeline-cursor" style={{ left: `${cursorPct}%` }} />
      </div>
      <div className="player-timeline-axis" data-testid="player-timeline-axis">
        {ticks.map((tick) => (
          <span key={tick.ns.toString()} className="player-timeline-tick" style={{ left: `${tick.leftPct}%` }}>
            {tick.label}
          </span>
        ))}
      </div>
    </>
  )
}
