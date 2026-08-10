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
    setLiveState(newLiveTreeState())
    setContentBytes(0)
    setEstimatedTocBytes(0)
    setPrevUUID(null)
  }, [storage])

  // Bootstrap the "previous fblock" link from candidates() -- before the
  // first fblock.ready arrives over WS, this is the only way to know what
  // this storage's last-written fblock was. fblock.ready is a storage-level
  // event (one fcontainer commonly holds every channel of a storage at
  // once, docs/docs/archive/adr/014-channel-registry.md), so there's no
  // single "the channel" to ask -- picking any one channel of this storage
  // is a best-effort seed, same imprecision this link always had.
  const firstChannel = channelsForStorage[0]?.channel

  useEffect(() => {
    if (!storage || firstChannel === undefined) return
    const now = BigInt(Date.now()) * 1_000_000n
    candidates(storage, firstChannel, 0n, now)
      .then((rows) => {
        if (rows.length === 0) return
        const latest = rows.reduce((a, b) => (b.end > a.end ? b : a))
        setPrevUUID((cur) => cur ?? latest.uuid)
      })
      .catch((e) => setError(String(e)))
  }, [storage, firstChannel])

  useEffect(() => {
    if (!storage) return

    function onLive(msg: LivePushMessage) {
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
      if (e.storage !== storage) return
      if (e.name === 'fblock.created') {
        // The storage's shared segment just flushed and reopened a fresh
        // one -- the old tree is now fully superseded by its own
        // fblock.ready below, so the live tree resets to track the new
        // fcontainer from scratch.
        setLiveState(newLiveTreeState())
        setContentBytes(0)
        setEstimatedTocBytes(0)
      } else if (e.name === 'fblock.ready' && e.uuid) {
        setPrevUUID(e.uuid)
      }
    }

    return subscribeLive(storage, onLive, onEvent, setConnected)
  }, [storage])

  const geometry = storages.find((s) => s.id === storage)?.geometry

  return (
    <section>
      <h1 className="mb-3">Fblock (live)</h1>

      <div className="card mb-3">
        <div className="card-body">
          <div className="row g-3 align-items-end">
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                storage
                <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
                  {storages.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.id}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="col-md-4">
              <span className={`badge ${connected ? 'text-bg-success' : 'text-bg-secondary'}`}>
                {connected ? 'подключено' : 'не подключено'}
              </span>
            </div>
            <div className="col-md-4 text-md-end">
              {prevUUID && storage && (
                <Link to={`/fblock-status?storage=${encodeURIComponent(storage)}&uuid=${prevUUID}`}>предыдущий fblock →</Link>
              )}
            </div>
          </div>
        </div>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      {storage && geometry && (
        <FblockFillBar
          fblockSize={geometry.FblockSize}
          maxChannels={geometry.MaxChannels}
          catalogN={geometry.N}
          contentBytes={contentBytes}
          estimatedTocBytes={estimatedTocBytes}
        />
      )}

      {storage && <FblockTree mode="live" state={liveState} />}
    </section>
  )
}
