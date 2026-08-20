import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { deleteStorage, listStorages, patchStorage, type StorageInfo } from '../../api/farcd'

const WRITE_MODES = ['cyclic', 'fill_until_full'] as const
const GiB = 1024 ** 3

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

export default function StorageEditPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [storage, setStorage] = useState<StorageInfo | null | undefined>(undefined) // undefined = loading
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)

  const [retentionDays, setRetentionDays] = useState(30)
  const [writeMode, setWriteMode] = useState('')
  const [name, setName] = useState('')
  const [poolSize, setPoolSize] = useState(4)
  const [poolWarningAt, setPoolWarningAt] = useState(2)
  const [poolBackpressureAt, setPoolBackpressureAt] = useState(4)

  useEffect(() => {
    listStorages()
      .then((all) => {
        const found = all.find((s) => s.id === id) ?? null
        setStorage(found)
        if (found) {
          setName(found.name ?? '')
          // Unlike retentionDays/writeMode above, GET /storages does report
          // the real pool tuning in effect (issues 02/03) -- prefill from
          // it rather than a UI default.
          setPoolSize(found.pool.Size)
          setPoolWarningAt(found.pool.WarningAt)
          setPoolBackpressureAt(found.pool.BackpressureAt)
        }
      })
      .catch((e) => setError(String(e)))
  }, [id])

  // farcd's GET /storages never echoes retention_days/write_mode back
  // (StorageInfo only carries id/path/geometry) -- there's nothing to
  // preselect these controls with, so they start at sensible defaults
  // rather than the storage's actual current values.
  async function onSaveRetention() {
    setError(null)
    setStatus(null)
    try {
      await patchStorage(id!, { retention_days: retentionDays })
      setStatus(`retention set to ${retentionDays} days`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onSaveName() {
    setError(null)
    setStatus(null)
    try {
      await patchStorage(id!, { name })
      setStatus(`name set to "${name}"`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onSaveWriteMode(mode: string) {
    setWriteMode(mode)
    setError(null)
    setStatus(null)
    try {
      await patchStorage(id!, { write_mode: mode })
      setStatus(`write mode set to ${mode}`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onSavePool() {
    setError(null)
    setStatus(null)
    try {
      await patchStorage(id!, {
        pool: { Size: poolSize, WarningAt: poolWarningAt, BackpressureAt: poolBackpressureAt },
      })
      setStatus('pool tuning saved (takes effect after farcd restart)')
    } catch (e) {
      setError(String(e))
    }
  }

  async function onDetach() {
    if (!window.confirm(`Detach storage "${id}"? This permanently destroys its data on disk.`)) {
      return
    }
    setError(null)
    setStatus(null)
    try {
      await deleteStorage(id!)
      navigate('/storages')
    } catch (e) {
      setError(String(e))
    }
  }

  if (storage === null) {
    return (
      <section>
        <h1 className="mb-3">Storage not found</h1>
        <p>No storage with id "{id}" exists.</p>
        <Link to="/storages" className="btn btn-outline-secondary">
          ← Back to storages
        </Link>
      </section>
    )
  }

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Edit storage {id}</h1>
        <Link to="/storages" className="btn btn-outline-secondary">
          ← Back to storages
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}
      {status && <div className="alert alert-success">{status}</div>}

      {storage === undefined ? (
        <p className="text-body-secondary">Loading…</p>
      ) : (
        <>
          <dl className="row">
            <dt className="col-sm-3">path</dt>
            <dd className="col-sm-9">{storage.path}</dd>
            <dt className="col-sm-3">size</dt>
            <dd className="col-sm-9">{formatGiB(storage.geometry.FblockSize * storage.geometry.N)} GiB</dd>
            <dt className="col-sm-3">fblock size</dt>
            <dd className="col-sm-9">{formatBytes(storage.geometry.FblockSize)}</dd>
            <dt className="col-sm-3">max channels</dt>
            <dd className="col-sm-9">{storage.geometry.MaxChannels}</dd>
          </dl>

          <div className="card">
            <div className="card-body">
              <h2 className="card-title h5">Mutable settings</h2>
              <p className="card-text text-body-secondary">
                Everything above is fixed at creation time. Retention, write mode, name, and pool tuning can still
                change.
              </p>
              <div className="d-flex flex-column gap-3">
                <div>
                  <label className="form-label">
                    name
                    <div className="input-group">
                      <input className="form-control" value={name} onChange={(e) => setName(e.target.value)} />
                      <button type="button" className="btn btn-outline-secondary" onClick={onSaveName}>
                        Save
                      </button>
                    </div>
                  </label>
                </div>
                <div>
                  <label className="form-label">
                    retention (days)
                    <div className="input-group">
                      <input
                        type="number"
                        className="form-control"
                        value={retentionDays}
                        onChange={(e) => setRetentionDays(Number(e.target.value))}
                      />
                      <button type="button" className="btn btn-outline-secondary" onClick={onSaveRetention}>
                        Save
                      </button>
                    </div>
                  </label>
                </div>
                <div>
                  <label className="form-label">
                    write mode
                    <select
                      className="form-select"
                      value={writeMode}
                      onChange={(e) => onSaveWriteMode(e.target.value)}
                    >
                      <option value="" disabled>
                        choose…
                      </option>
                      {WRITE_MODES.map((m) => (
                        <option key={m} value={m}>
                          {m}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <div>
                  <label className="form-label">
                    pool size (fblocks in RAM)
                    <input
                      type="number"
                      className="form-control"
                      value={poolSize}
                      onChange={(e) => setPoolSize(Number(e.target.value))}
                    />
                  </label>
                  <div className="form-text">
                    ≈ {formatGiB(poolSize * storage.geometry.FblockSize)} GiB RAM when the pool is full
                  </div>
                </div>
                <div>
                  <label className="form-label">
                    pool warning at
                    <input
                      type="number"
                      className="form-control"
                      value={poolWarningAt}
                      onChange={(e) => setPoolWarningAt(Number(e.target.value))}
                    />
                  </label>
                  <div className="form-text">
                    {poolSize === 0
                      ? '—'
                      : `${poolWarningAt} of ${poolSize} slots (${Math.round((100 * poolWarningAt) / poolSize)}%)`}
                  </div>
                </div>
                <div>
                  <label className="form-label">
                    pool backpressure at
                    <input
                      type="number"
                      className="form-control"
                      value={poolBackpressureAt}
                      onChange={(e) => setPoolBackpressureAt(Number(e.target.value))}
                    />
                  </label>
                  <div className="form-text">
                    {poolSize === 0
                      ? '—'
                      : `${poolBackpressureAt} of ${poolSize} slots (${Math.round((100 * poolBackpressureAt) / poolSize)}%)`}
                  </div>
                </div>
                <div>
                  <button type="button" className="btn btn-outline-secondary" onClick={onSavePool}>
                    Save pool tuning
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div className="card border-danger mt-3">
            <div className="card-body">
              <h2 className="card-title h5">Danger zone</h2>
              <p className="card-text text-body-secondary">
                Detaching permanently destroys this storage's data on disk. Every attached channel must be removed
                first.
              </p>
              <button type="button" className="btn btn-outline-danger" onClick={onDetach}>
                Detach
              </button>
            </div>
          </div>
        </>
      )}
    </section>
  )
}
