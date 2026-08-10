import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { listFblocks, listStorages, type StorageInfo } from '../api/farcd'
import { nsToDisplayString, type FblockInfo } from '../api/ns'
import FblockTree from '../components/FblockTree'

export default function FblockStatusPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const uuid = searchParams.get('uuid') ?? ''

  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState(searchParams.get('storage') ?? '')
  const [fblocks, setFblocks] = useState<FblockInfo[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then((s) => {
        setStorages(s)
        if (s.length > 0) setStorage((cur) => cur || s[0].id)
      })
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    if (!storage) return
    listFblocks(storage)
      .then(setFblocks)
      .catch((e) => setError(String(e)))
  }, [storage])

  function onStorageChange(next: string) {
    setStorage(next)
    setSearchParams(next ? { storage: next } : {})
  }

  return (
    <section>
      <h1 className="mb-3">Fblock (status)</h1>

      <div className="card mb-3">
        <div className="card-body">
          <label className="form-label">
            storage
            <select className="form-select" value={storage} onChange={(e) => onStorageChange(e.target.value)}>
              {storages.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.id}
                </option>
              ))}
            </select>
          </label>
        </div>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      <div className="table-responsive mb-3">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>index</th>
              <th>state</th>
              <th>uuid</th>
              <th>begin</th>
              <th>end</th>
              <th>protected</th>
              <th>channels</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {fblocks.map((fb) => (
              <tr key={fb.index} className={fb.uuid === uuid ? 'table-active' : ''}>
                <td>{fb.index}</td>
                <td>{fb.state}</td>
                <td>{fb.uuid ?? '—'}</td>
                <td>{fb.begin !== undefined ? nsToDisplayString(fb.begin) : '—'}</td>
                <td>{fb.end !== undefined ? nsToDisplayString(fb.end) : '—'}</td>
                <td>{fb.state === 'ready' ? (fb.protected ? 'yes' : 'no') : '—'}</td>
                <td>{fb.channels?.join(', ') ?? '—'}</td>
                <td>
                  {fb.state === 'ready' && fb.uuid ? (
                    <Link
                      to={`/fblock-status?storage=${encodeURIComponent(storage)}&uuid=${fb.uuid}`}
                      className="btn btn-outline-secondary btn-sm"
                    >
                      Open
                    </Link>
                  ) : (
                    '—'
                  )}
                </td>
              </tr>
            ))}
            {fblocks.length === 0 && (
              <tr>
                <td colSpan={8} className="text-body-secondary">
                  no fblocks yet
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {storage && uuid && <FblockTree mode="status" storage={storage} uuid={uuid} />}
    </section>
  )
}
