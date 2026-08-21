import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listChannels, type ChannelInfo } from '../api/farcd'
import { getLiveURLs } from '../api/apid'
import ChannelStatusIndicator from '../components/ChannelStatusIndicator'
import LiveVideoTile from '../components/LiveVideoTile'
import VideoGrid from '../components/VideoGrid'

// Which channels are currently shown in the live grid -- persisted across
// visits (empty on the very first one), never defaulted to "all checked"
// (.scratch/live-page/spec.md: an unbounded number of simultaneous WebRTC
// sessions on every page load is the thing this avoids).
const STORAGE_KEY = 'farc.live-page.checked-channels'

function loadChecked(): Set<number> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return new Set()
    const ids = JSON.parse(raw)
    return Array.isArray(ids) ? new Set(ids) : new Set()
  } catch {
    return new Set()
  }
}

function saveChecked(checked: Set<number>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Array.from(checked)))
  } catch {
    // localStorage unavailable (private mode, quota) -- not fatal, just no persistence.
  }
}

export default function LivePage() {
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [checked, setChecked] = useState<Set<number>>(() => loadChecked())
  const [liveUrls, setLiveUrls] = useState<Record<number, string>>({})
  const [activeChannel, setActiveChannel] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listChannels().then(setChannels, (e) => setError(String(e)))
  }, [])

  const channelIds = Array.from(checked).sort((a, b) => a - b)
  // Batch-fetch once per change to the *set* of checked channels, not once
  // per channel and not on every render -- joined to a stable string so
  // the effect below doesn't see a "new" array identity every render.
  const channelIdsKey = channelIds.join(',')

  useEffect(() => {
    if (channelIds.length === 0) {
      setLiveUrls({})
      return
    }
    getLiveURLs(channelIds).then(setLiveUrls, (e) => setError(String(e)))
    // channelIds is derived from channelIdsKey each render; depending on
    // the key alone is what actually avoids a refetch loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelIdsKey])

  function toggleChannel(channel: number) {
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(channel)) next.delete(channel)
      else next.add(channel)
      saveChecked(next)
      return next
    })
  }

  return (
    <section>
      <h1 className="mb-3">Live</h1>
      {error && <div className="alert alert-danger">{error}</div>}

      <div className="row g-3">
        <div className="col-md-3">
          <ul className="list-unstyled">
            {channels.map((c) => (
              <li key={c.channel} className="live-channel-row mb-2">
                <input
                  type="checkbox"
                  className="form-check-input"
                  aria-label={`show channel ${c.channel}`}
                  checked={checked.has(c.channel)}
                  onChange={() => toggleChannel(c.channel)}
                />
                <ChannelStatusIndicator channel={c.channel} connected={!c.last_connect_error} recording={!!c.recording} />
                <span>
                  {c.channel}: {c.name ?? `channel ${c.channel}`}
                </span>
                <Link to={`/player?channel=${c.channel}`} className="btn btn-sm btn-outline-secondary ms-auto">
                  в архив
                </Link>
              </li>
            ))}
          </ul>
        </div>

        <div className="col-md-9">
          <VideoGrid
            channelIds={channelIds}
            Tile={LiveVideoTile}
            getTileProps={(channel) => ({
              channel,
              whepUrl: liveUrls[channel] ?? null,
              muted: activeChannel !== channel,
              active: activeChannel === channel,
              onClick: () => setActiveChannel(channel),
            })}
          />
        </div>
      </div>
    </section>
  )
}
