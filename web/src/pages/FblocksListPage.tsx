import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listStorages, type StorageInfo } from '../api/farcd'
import { getCatalog, type CatalogEntry } from '../api/fblockTree'
import { nsToDisplayString } from '../api/ns'
import { pageOf, totalPages, visibleEntries } from './fblocksListPaging'

const PAGE_SIZE = 50

// FblocksListPage is the searchable/tabular counterpart to "fblocks status"
// (FblocksGridPage): a table (index/state+protected/begin-end -- no uuid,
// no size, both settled as redundant per-row during grilling) with client-
// side pagination + a state filter, since the catalog is already fetched
// in one cheap request either way. Each row links to the fblock-tree page.
export default function FblocksListPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [entries, setEntries] = useState<CatalogEntry[]>([])
  const [hideUninitialized, setHideUninitialized] = useState(true)
  const [page, setPage] = useState(0)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then(setStorages)
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    setEntries([])
    setError(null)
    setPage(0)
    if (!id) return
    getCatalog(id)
      .then(setEntries)
      .catch((e) => setError(String(e)))
  }, [id])

  const visible = visibleEntries(entries, hideUninitialized)
  const pages = totalPages(visible.length, PAGE_SIZE)
  const shown = pageOf(visible, Math.min(page, pages - 1), PAGE_SIZE)

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Fblocks list</h1>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      <label className="d-block mb-3" style={{ maxWidth: '24rem' }}>
        storage
        <select className="form-select" value={id} onChange={(e) => navigate(`/storages/${e.target.value}/fblocks-list`)}>
          {storages.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name || s.id}
            </option>
          ))}
        </select>
      </label>

      <div className="form-check mb-3">
        <input
          className="form-check-input"
          type="checkbox"
          id="hide-uninitialized"
          checked={hideUninitialized}
          onChange={(e) => {
            setHideUninitialized(e.target.checked)
            setPage(0)
          }}
        />
        <label className="form-check-label" htmlFor="hide-uninitialized">
          hide uninitialized
        </label>
      </div>

      <table className="table table-sm">
        <thead>
          <tr>
            <th>index</th>
            <th>state</th>
            <th>begin</th>
            <th>end</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {shown.map((e) => (
            <tr key={e.index}>
              <td>#{e.index}</td>
              <td>
                {e.state}
                {e.protected && <span className="badge text-bg-info ms-2">protected</span>}
              </td>
              <td>{e.begin ? nsToDisplayString(BigInt(e.begin)) : ''}</td>
              <td>{e.end ? nsToDisplayString(BigInt(e.end)) : ''}</td>
              <td>
                <Link to={`/storages/${id}/fblocks/${e.index}/tree`} className="btn btn-sm btn-outline-primary" aria-label={`#${e.index} tree`}>
                  tree
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {visible.length === 0 && !error && <div className="text-body-secondary">no fblocks</div>}

      {pages > 1 && (
        <div className="btn-group btn-group-sm">
          <button type="button" className="btn btn-outline-secondary" disabled={page <= 0} onClick={() => setPage((p) => Math.max(0, p - 1))}>
            Prev
          </button>
          <span className="btn btn-outline-secondary disabled">
            page {page + 1} / {pages}
          </span>
          <button type="button" className="btn btn-outline-secondary" disabled={page >= pages - 1} onClick={() => setPage((p) => Math.min(pages - 1, p + 1))}>
            Next
          </button>
        </div>
      )}
    </section>
  )
}
