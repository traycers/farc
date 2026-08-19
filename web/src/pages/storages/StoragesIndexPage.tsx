import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listStorages, type StorageInfo } from '../../api/farcd'

const GiB = 1024 ** 3

// GiB (1024-based), not GB (1e9-based) -- matches StorageNewPage's own
// formatting so a storage's size reads the same on both pages.
function formatGiB(bytes: number): string {
  return (bytes / GiB).toFixed(2)
}

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

// Auto-scaling 1024-based formatter for a single fblock size (typically MiB-GiB),
// distinct from formatGiB above which is for whole-storage totals.
function formatBytes(bytes: number): string {
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < BYTE_UNITS.length - 1) {
    value /= 1024
    unitIndex++
  }
  return `${unitIndex === 0 ? value : value.toFixed(2)} ${BYTE_UNITS[unitIndex]}`
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
              <th>name</th>
              <th>path</th>
              <th>size</th>
              <th>fblock size</th>
              <th>max channels</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {storages.map((s) => (
              <tr key={s.id}>
                <td>{s.name}</td>
                <td>{s.path}</td>
                <td>{formatGiB(s.geometry.FblockSize * s.geometry.N)} GiB</td>
                <td>{formatBytes(s.geometry.FblockSize)}</td>
                <td>{s.geometry.MaxChannels}</td>
                <td>
                  <div className="btn-group btn-group-sm">
                    <Link to={`${s.id}/edit`} className="btn btn-outline-secondary">
                      Edit
                    </Link>
                    <Link to={`${s.id}/fblocks`} className="btn btn-outline-primary">
                      fblocks status
                    </Link>
                    <Link to={`${s.id}/fblocks-list`} className="btn btn-outline-primary">
                      fblocks list
                    </Link>
                  </div>
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
