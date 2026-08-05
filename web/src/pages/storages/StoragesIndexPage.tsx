import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listStorages, type StorageInfo } from '../../api/farcd'

const GiB = 1024 ** 3

// GiB (1024-based), not GB (1e9-based) -- matches StorageNewPage's own
// formatting so a storage's size reads the same on both pages.
function formatGiB(bytes: number): string {
  return (bytes / GiB).toFixed(2)
}

export default function StoragesIndexPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then(setStorages)
      .catch((e) => setError(String(e)))
  }, [])

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Storages</h1>
        <Link to="new" className="btn btn-primary">
          New storage
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      <div className="table-responsive">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>id</th>
              <th>path</th>
              <th>size</th>
              <th>fblock size × N</th>
              <th>max channels</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {storages.map((s) => (
              <tr key={s.id}>
                <td>{s.id}</td>
                <td>{s.path}</td>
                <td>{formatGiB(s.geometry.FblockSize * s.geometry.N)} GiB</td>
                <td>
                  {s.geometry.FblockSize} × {s.geometry.N}
                </td>
                <td>{s.geometry.MaxChannels}</td>
                <td>
                  <Link to={`${s.id}/edit`} className="btn btn-outline-secondary btn-sm">
                    Edit
                  </Link>
                </td>
              </tr>
            ))}
            {storages.length === 0 && (
              <tr>
                <td colSpan={6} className="text-body-secondary">
                  no storages yet
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
