import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { candidates, listChannels, listStorages, type ChannelInfo, type StorageInfo } from '../api/farcd'
import { subscribeLive, type JournalEvent, type LivePushMessage } from '../api/events'
import FblockTree, { applyLiveNodes, clearNewIds, newLiveTreeState, type LiveTreeState } from '../components/FblockTree'
import FblockFillBar from '../components/FblockFillBar'

// How long a newly-arrived node stays highlighted green before
// FblockTree.tsx's CSS transition fades it back -- see clearNewIds.
const NEW_NODE_HIGHLIGHT_MS = 3000

export default function FblockLivePage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [channel, setChannel] = useState<number | null>(null)
  const [liveState, setLiveState] = useState<LiveTreeState>(newLiveTreeState())
  const [contentBytes, setContentBytes] = useState(0)
  const [estimatedTocBytes, setEstimatedTocBytes] = useState(0)
  const [prevUUID, setPrevUUID] = useState<string | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const timers = useRef<Set<ReturnType<typeof setTimeout>>>(new Set())

  useEffect(() => {
    listStorages()
      .then((s) => {
        setStorages(s)
        if (s.length > 0) setStorage((cur) => cur || s[0].id)
      })
      .catch((e) => setError(String(e)))
    listChannels()
      .then(setChannels)
      .catch((e) => setError(String(e)))
    return () => {
      for (const t of timers.current) clearTimeout(t)
    }
  }, [])

  const channelsForStorage = channels.filter((c) => c.storage === storage)

  useEffect(() => {
    if (channel === null && channelsForStorage.length > 0) setChannel(channelsForStorage[0].channel)
  }, [channel, channelsForStorage])

  // Bootstrap the "previous fblock" link from candidates() -- before the
  // first fblock.ready arrives over WS, this is the only way to know what
  // the channel's last-written fblock was.
  useEffect(() => {
    if (!storage || channel === null) return
    setPrevUUID(null)
    const now = BigInt(Date.now()) * 1_000_000n
    candidates(storage, channel, 0n, now)
      .then((rows) => {
        if (rows.length === 0) return
        const latest = rows.reduce((a, b) => (b.end > a.end ? b : a))
        setPrevUUID(latest.uuid)
      })
      .catch((e) => setError(String(e)))
  }, [storage, channel])

  useEffect(() => {
    if (channel === null) return
    setLiveState(newLiveTreeState())
    setContentBytes(0)
    setEstimatedTocBytes(0)

    function onLive(msg: LivePushMessage) {
      if (msg.channel !== channel) return
      setLiveState((prev) => applyLiveNodes(prev, msg.nodes))
      setContentBytes(msg.content_bytes)
      setEstimatedTocBytes(msg.estimated_toc_bytes)
      const ids = msg.nodes.map((n) => n.id)
      const timer = setTimeout(() => {
        setLiveState((prev) => clearNewIds(prev, ids))
        timers.current.delete(timer)
      }, NEW_NODE_HIGHLIGHT_MS)
      timers.current.add(timer)
    }

    function onEvent(e: JournalEvent) {
      if (e.channel !== channel) return
      if (e.name === 'fblock.created') {
        // A new segment just started recording -- the old one (if any) is
        // now fully superseded by its own fblock.ready below, so the live
        // tree resets to track the new segment from scratch. Creation-order
        // ids restart at 0 each segment, so keeping stale entries around
        // would silently corrupt frame counts/parent links.
        setLiveState(newLiveTreeState())
        setContentBytes(0)
        setEstimatedTocBytes(0)
      } else if (e.name === 'fblock.ready' && e.storage === storage && e.uuid) {
        setPrevUUID(e.uuid)
      }
    }

    return subscribeLive(channel, onLive, onEvent, setConnected)
  }, [channel, storage])

  const geometry = storages.find((s) => s.id === storage)?.geometry

  return (
    <section>
      <h1 className="mb-3">Fblock (live)</h1>

      <div className="card mb-3">
        <div className="card-body">
          <div className="row g-3 align-items-end">
            <div className="col-sm-6 col-md-3">
              <label className="form-label">
                storage
                <select
                  className="form-select"
                  value={storage}
                  onChange={(e) => {
                    setStorage(e.target.value)
                    setChannel(null)
                  }}
                >
                  {storages.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.id}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="col-sm-6 col-md-3">
              <label className="form-label">
                channel
                <select className="form-select" value={channel ?? ''} onChange={(e) => setChannel(Number(e.target.value))}>
                  {channelsForStorage.map((c) => (
                    <option key={c.channel} value={c.channel}>
                      {c.name ? `${c.channel} — ${c.name}` : c.channel}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="col-md-3">
              <span className={`badge ${connected ? 'text-bg-success' : 'text-bg-secondary'}`}>
                {connected ? 'подключено' : 'не подключено'}
              </span>
            </div>
            <div className="col-md-3 text-md-end">
              {prevUUID && storage && (
                <Link to={`/fblock-status?storage=${encodeURIComponent(storage)}&uuid=${prevUUID}`}>предыдущий fblock →</Link>
              )}
            </div>
          </div>
        </div>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      {channel !== null && geometry && (
        <FblockFillBar
          fblockSize={geometry.FblockSize}
          maxChannels={geometry.MaxChannels}
          catalogN={geometry.N}
          contentBytes={contentBytes}
          estimatedTocBytes={estimatedTocBytes}
        />
      )}

      {channel !== null && <FblockTree mode="live" state={liveState} />}
    </section>
  )
}
