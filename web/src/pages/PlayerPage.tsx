import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import ChannelChecklist from '../components/ChannelChecklist'
import TimelineBar from '../components/TimelineBar'
import VideoGrid from '../components/VideoGrid'
import VideoTile from '../components/VideoTile'
import { listChannels, type ChannelInfo } from '../api/farcd'
import { getTimeline, playlistUrl, type ChannelTimeline } from '../api/hls'
import { nsFromLocalInputValue, nsToDisplayString, nsToLocalInputValue } from '../api/ns'
import { advance, computeDataRange, hasAnySegments, nextSegmentStart, prevSegmentStart } from './playerTimeline'

const ONE_HOUR_NS = 3_600_000_000_000n
const TICK_MS = 200

function segmentAt(ct: ChannelTimeline | undefined, t: bigint) {
  return ct?.segments.find((s) => s.begin <= t && t <= s.end) ?? null
}

// PlayerPage: multi-channel archive player (.scratch/player-redesign/
// spec.md) -- fully replaces the old single-channel page. Orchestration
// only: search form, checklist, the one shared playhead, play/stop/prev/
// next. Anything beyond gluing ChannelChecklist/TimelineBar/VideoGrid
// together belongs in one of pages/playerLayout.ts, pages/playerTimeline.ts
// or pages/timelineGeometry.ts instead.
export default function PlayerPage() {
  const [searchParams] = useSearchParams()
  const autoSubmittedRef = useRef(false)

  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [checked, setChecked] = useState<Set<number>>(new Set())

  const now = BigInt(Date.now()) * 1_000_000n
  const [from, setFrom] = useState(nsToLocalInputValue(now - ONE_HOUR_NS))
  const [to, setTo] = useState(nsToLocalInputValue(now))

  const [timelines, setTimelines] = useState<ChannelTimeline[] | null>(null)
  const [rangeStart, setRangeStart] = useState(0n)
  const [rangeEnd, setRangeEnd] = useState(0n)
  const [playheadNs, setPlayheadNs] = useState(0n)
  const [playing, setPlaying] = useState(false)
  const [activeChannel, setActiveChannel] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  const lastTickRef = useRef(0)

  useEffect(() => {
    listChannels().then(setChannels, (e) => setError(String(e)))
  }, [])

  // Jump-from-Live (.scratch/live-page/issues/06): a `?channel=` param
  // pre-checks that channel and runs the search immediately, so following
  // a Live-page link lands straight on video instead of an empty form --
  // `from`/`to` need no special-casing, they already default to the last
  // hour. Waits for channels to load so the param can be validated against
  // the real list; autoSubmittedRef ensures this fires at most once, not
  // on every channels/searchParams re-render.
  useEffect(() => {
    if (autoSubmittedRef.current || channels.length === 0) return
    const param = searchParams.get('channel')
    if (!param) return
    const channel = Number(param)
    if (!channels.some((c) => c.channel === channel)) return
    autoSubmittedRef.current = true
    setChecked(new Set([channel]))
    runSearch([channel])
  }, [channels, searchParams])

  useEffect(() => {
    if (!playing || !timelines) return
    lastTickRef.current = Date.now()
    const id = setInterval(() => {
      const now = Date.now()
      const deltaNs = BigInt(now - lastTickRef.current) * 1_000_000n
      lastTickRef.current = now
      setPlayheadNs((prev) => {
        const t = prev + deltaNs
        const step = advance(timelines, t)
        if (step.kind === 'end') {
          setPlaying(false)
          return prev
        }
        if (step.kind === 'skip') return step.to
        return t
      })
    }, TICK_MS)
    return () => clearInterval(id)
  }, [playing, timelines])

  function toggleChannel(channel: number) {
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(channel)) next.delete(channel)
      else next.add(channel)
      return next
    })
  }

  async function runSearch(channelIds: number[]) {
    setError(null)
    setPlaying(false)
    try {
      const t1 = nsFromLocalInputValue(from)
      const t2 = nsFromLocalInputValue(to)
      const result = await getTimeline(channelIds, t1, t2)
      if (!hasAnySegments(result)) {
        setTimelines(null)
        setError('No records found in the selected range')
        return
      }
      setTimelines(result)
      const range = computeDataRange(result, t1, t2)
      setRangeStart(range.start)
      setRangeEnd(range.end)
      setPlayheadNs(range.start)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onSearch(e: React.FormEvent) {
    e.preventDefault()
    await runSearch(Array.from(checked))
  }

  function jumpTo(ns: bigint | null) {
    if (ns !== null) setPlayheadNs(ns)
  }

  const channelIds = Array.from(checked)

  return (
    <section>
      <h1 className="mb-3">Player</h1>

      <div className="row g-3">
        <div className="col-md-3">
          <ChannelChecklist channels={channels} checked={checked} onToggle={toggleChannel} />

          <form onSubmit={onSearch} className="mt-3">
            <label className="form-label">
              from
              <input
                type="datetime-local"
                className="form-control"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </label>
            <label className="form-label">
              to
              <input type="datetime-local" className="form-control" value={to} onChange={(e) => setTo(e.target.value)} />
            </label>
            <button type="submit" className="btn btn-primary w-100 mt-2" disabled={checked.size === 0}>
              Search
            </button>
          </form>
        </div>

        <div className="col-md-9 player-page">
          {error && <div className="alert alert-danger">{error}</div>}

          <VideoGrid
            channelIds={channelIds}
            Tile={VideoTile}
            getTileProps={(channel) => {
              const ct = timelines?.find((t) => t.channel === channel)
              const segment = segmentAt(ct, playheadNs)
              return {
                channel,
                segmentUrl: segment ? playlistUrl(channel, segment.begin, segment.end) : null,
                seekToSec: segment ? Number(playheadNs - segment.begin) / 1e9 : 0,
                playing,
                muted: activeChannel !== channel,
                active: activeChannel === channel,
                onClick: () => setActiveChannel(channel),
              }
            }}
          />

          {timelines && (
            <>
              <div className="mt-2" data-testid="player-current-time" data-playhead-ns={playheadNs.toString()}>
                текущее время: {nsToDisplayString(playheadNs)}
              </div>
              <TimelineBar
                timelines={timelines}
                rangeStart={rangeStart}
                rangeEnd={rangeEnd}
                playheadNs={playheadNs}
                onSeek={setPlayheadNs}
              />
              <div className="btn-group mt-2">
                <button type="button" className="btn btn-secondary" onClick={() => jumpTo(prevSegmentStart(timelines, playheadNs))}>
                  Prev
                </button>
                <button type="button" className="btn btn-primary" onClick={() => setPlaying(true)}>
                  Play
                </button>
                <button type="button" className="btn btn-secondary" onClick={() => setPlaying(false)}>
                  Stop
                </button>
                <button type="button" className="btn btn-secondary" onClick={() => jumpTo(nextSegmentStart(timelines, playheadNs))}>
                  Next
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </section>
  )
}
