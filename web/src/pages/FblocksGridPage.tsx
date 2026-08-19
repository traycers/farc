import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listStorages, type StorageInfo } from '../api/farcd'
import { subscribeStorageEvents, type JournalEvent } from '../api/events'
import { getCatalog, getFblockInfo, type CatalogEntry } from '../api/fblockTree'
import type { PoolSlot } from '../api/pool'
import PoolStatusList from '../components/PoolStatusList'

// LIVE_WANT mirrors internal/storage/notify.go's own Event names (not the
// bridged journal ones -- see subscribeStorageEvents), covering every
// transition that can change a square's color or clear it back to
// uninitialized.
const LIVE_WANT = ['fblock.write.started', 'fblock.write.completed', 'fblock.write.failed', 'fblock.deleted']

function stateClass(state: string): string {
  switch (state) {
    case 'in_progress':
      return 'state-in_progress'
    case 'ready':
      return 'state-ready'
    case 'bad':
      return 'state-bad'
    default:
      return ''
  }
}

// FblocksGridPage shows every fblock in a storage as a GitHub-profile-style
// grid of same-size squares (color = state, purple border = protected),
// reached via a button on StoragesIndexPage with the storage pre-selected.
// Initial load is the bulk GET .../catalog; from then on a per-storage WS
// subscription (subscribeStorageEvents) tells this page which single index
// changed, and it re-fetches just that one via GET .../fblocks/{index} --
// no polling, no re-fetching the whole catalog per event.
export default function FblocksGridPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [entries, setEntries] = useState<Map<number, CatalogEntry>>(new Map())
  const [poolSlots, setPoolSlots] = useState<PoolSlot[]>([])
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then(setStorages)
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    setEntries(new Map())
    setPoolSlots([])
    setError(null)
    if (!id) return

    getCatalog(id)
      .then((list) => setEntries(new Map(list.map((e) => [e.index, e]))))
      .catch((e) => setError(String(e)))

    function onEvent(ev: JournalEvent) {
      if (ev.index === undefined) return
      getFblockInfo(id, ev.index)
        .then((info) => {
          setEntries((prev) => {
            const next = new Map(prev)
            next.set(info.index, {
              index: info.index,
              state: info.state,
              uuid: info.uuid,
              begin: info.begin,
              end: info.end,
              protected: info.protected,
            })
            return next
          })
        })
        .catch(() => {
          // a transient re-fetch failure just leaves the square at its
          // last known state until the next event corrects it
        })
    }

    const unsubscribe = subscribeStorageEvents(id, LIVE_WANT, onEvent, setConnected, {
      includePool: true,
      onPool: (msg) => setPoolSlots(msg.slots),
    })
    return unsubscribe
  }, [id])

  const sorted = [...entries.values()].sort((a, b) => a.index - b.index)
  const fblockSize = storages.find((s) => s.id === id)?.geometry.FblockSize ?? 0

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Fblocks status</h1>
        <span className={`badge ${connected ? 'text-bg-success' : 'text-bg-warning'}`}>
          {connected ? 'connected' : 'reconnecting…'}
        </span>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      <label className="d-block mb-3" style={{ maxWidth: '24rem' }}>
        storage
        <select className="form-select" value={id} onChange={(e) => navigate(`/storages/${e.target.value}/fblocks`)}>
          {storages.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name || s.id}
            </option>
          ))}
        </select>
      </label>

      {poolSlots.length > 0 && <PoolStatusList slots={poolSlots} fblockSize={fblockSize} />}

      <div className="fblock-grid">
        {sorted.map((entry) => {
          const cls = `fblock-square ${stateClass(entry.state)}${entry.protected ? ' protected' : ''}`
          const title = `#${entry.index} ${entry.state}${entry.protected ? ' (protected)' : ''}`
          return entry.state === 'ready' || entry.state === 'in_progress' ? (
            <Link key={entry.index} to={`/storages/${id}/fblocks/${entry.index}/tree`} className={cls} title={title} />
          ) : (
            <div key={entry.index} className={cls} title={title} />
          )
        })}
      </div>
      {sorted.length === 0 && !error && <div className="text-body-secondary">no fblocks</div>}
    </section>
  )
}
