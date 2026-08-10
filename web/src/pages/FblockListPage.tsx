import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { listFblocks, listStorages, type StorageInfo } from '../api/farcd'
import { nsToDisplayString, type FblockInfo } from '../api/ns'

const PAGE_SIZE = 100

export default function FblockListPage() {
  const [searchParams, setSearchParams] = useSearchParams()

  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState(searchParams.get('storage') ?? '')
  const [page, setPage] = useState(0)
  const [total, setTotal] = useState(0)
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
    listFblocks(storage, page * PAGE_SIZE, PAGE_SIZE)
      .then((res) => {
        setFblocks(res.fblocks)
        setTotal(res.total)
      })
      .catch((e) => setError(String(e)))
  }, [storage, page])

  function onStorageChange(next: string) {
    setStorage(next)
    setPage(0)
    setSearchParams(next ? { storage: next } : {})
  }

  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <section>
      <h1 className="mb-3">Fblocks</h1>

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
              <tr key={fb.index}>
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

      <nav aria-label="fblocks pagination">
        <ul className="pagination">
          <li className={`page-item${page === 0 ? ' disabled' : ''}`}>
            <button className="page-link" onClick={() => setPage((p) => Math.max(0, p - 1))} disabled={page === 0}>
              Назад
            </button>
          </li>
          <li className="page-item disabled">
            <span className="page-link">
              стр. {page + 1} из {pageCount}
            </span>
          </li>
          <li className={`page-item${page + 1 >= pageCount ? ' disabled' : ''}`}>
            <button
              className="page-link"
              onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
              disabled={page + 1 >= pageCount}
            >
              Вперёд
            </button>
          </li>
        </ul>
      </nav>
    </section>
  )
}
