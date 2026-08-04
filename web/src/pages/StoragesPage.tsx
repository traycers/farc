import { useEffect, useState } from 'react'
import { createStorage, listStorages, patchStorage, type StorageInfo } from '../api/farcd'

const WRITE_MODES = ['cyclic', 'fill_until_full'] as const
const MiB = 1024 ** 2
const GiB = 1024 ** 3

// GiB (1024-based), not GB (1e9-based), and consistently so everywhere size
// is computed or shown -- fblock sizes are themselves power-of-two byte
// counts, so as long as the desired size is a whole number of fblocks (the
// common case, e.g. 100 GiB / 1 GiB fblocks), N divides evenly and the
// result matches exactly what was asked for, with zero rounding.
function formatGiB(bytes: number): string {
  return (bytes / GiB).toFixed(2)
}

export default function StoragesPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const [id, setId] = useState('')
  const [path, setPath] = useState('')
  // farcd's POST /storages has no separate "total size" field -- it creates
  // the file itself, sized to exactly FblockSize*N (handleCreateStorage).
  // desiredSizeGiB is purely a UI convenience: N is derived from it so the
  // operator thinks in "how big is this disk" rather than fblock count.
  const [fblockSizeMiB, setFblockSizeMiB] = useState(1024)
  const [desiredSizeGiB, setDesiredSizeGiB] = useState(100)
  const fblockSize = fblockSizeMiB * MiB
  const n = Math.max(1, Math.floor((desiredSizeGiB * GiB) / fblockSize))
  const actualSizeBytes = n * fblockSize
  const [maxChannels, setMaxChannels] = useState(8)
  const [fchunkSize, setFchunkSize] = useState(4 * 1024 * 1024)
  const [writeMode, setWriteMode] = useState<(typeof WRITE_MODES)[number]>('cyclic')
  const [retentionDays, setRetentionDays] = useState(30)
  const [minContainerShare, setMinContainerShare] = useState(0.1)
  const [force, setForce] = useState(false)

  const refresh = () => listStorages().then(setStorages).catch((e) => setError(String(e)))

  useEffect(() => {
    refresh()
  }, [])

  async function onCreate(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await createStorage({
        id,
        path,
        geometry: { FblockSize: fblockSize, N: n, MaxChannels: maxChannels },
        params: {
          fchunk_size: fchunkSize,
          write_mode: writeMode,
          retention: { days: retentionDays },
          min_container_share: minContainerShare,
        },
        force,
        catalog_path: '',
        backend: '',
      })
      setId('')
      setPath('')
      await refresh()
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  // PATCH returns 204 and GET /storages never echoes retention_days/
  // write_mode back (StorageInfo only carries id/path/geometry) -- there is
  // nothing to reflect in the table afterwards, so this just reports errors.
  async function onPatch(id: string, patch: { retention_days?: number; write_mode?: string }) {
    setError(null)
    try {
      await patchStorage(id, patch)
    } catch (e) {
      setError(String(e))
    }
  }

  return (
    <section>
      <h1 className="mb-3">Storages</h1>
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
              <th>set retention (days)</th>
              <th>set write mode</th>
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
                  <RetentionEditor onSave={(days) => onPatch(s.id, { retention_days: days })} />
                </td>
                <td>
                  <select
                    className="form-select form-select-sm"
                    onChange={(e) => onPatch(s.id, { write_mode: e.target.value })}
                    defaultValue=""
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
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card">
        <div className="card-body">
          <h2 className="card-title h4">Create storage</h2>
          <p className="card-text text-body-secondary">
            For a plain file path, farcd creates and sizes the file itself (to <code>desired size</code> below,
            rounded down to a whole number of fblocks) — nothing to pre-create. For a raw block device/partition
            path, the partition must already be at least that size. Either way, farcd persists the new entry into
            its own config file on creation, so it survives a restart.
          </p>
          <form onSubmit={onCreate} className="row g-3 align-items-end">
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                id
                <input className="form-control" value={id} onChange={(e) => setId(e.target.value)} required />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                path
                <input
                  className="form-control"
                  value={path}
                  onChange={(e) => setPath(e.target.value)}
                  placeholder="/data/disk0.img"
                  required
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                desired size (GiB)
                <input
                  type="number"
                  className="form-control"
                  value={desiredSizeGiB}
                  onChange={(e) => setDesiredSizeGiB(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                fblock size (MiB)
                <input
                  type="number"
                  className="form-control"
                  value={fblockSizeMiB}
                  onChange={(e) => setFblockSizeMiB(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-md-8">
              <div className="form-text">
                → N = {n} fblocks, actual size = {formatGiB(actualSizeBytes)} GiB
                {actualSizeBytes !== desiredSizeGiB * GiB &&
                  ' (not an exact multiple of the fblock size — rounded down)'}
              </div>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                max channels
                <input
                  type="number"
                  className="form-control"
                  value={maxChannels}
                  onChange={(e) => setMaxChannels(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                fchunk size (bytes)
                <input
                  type="number"
                  className="form-control"
                  value={fchunkSize}
                  onChange={(e) => setFchunkSize(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                write mode
                <select
                  className="form-select"
                  value={writeMode}
                  onChange={(e) => setWriteMode(e.target.value as (typeof WRITE_MODES)[number])}
                >
                  {WRITE_MODES.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                retention (days)
                <input
                  type="number"
                  className="form-control"
                  value={retentionDays}
                  onChange={(e) => setRetentionDays(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-4">
              <label className="form-label">
                min container share
                <input
                  type="number"
                  step="0.01"
                  className="form-control"
                  value={minContainerShare}
                  onChange={(e) => setMinContainerShare(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-md-4">
              <div className="form-check">
                <input
                  type="checkbox"
                  className="form-check-input"
                  id="force-reinit"
                  checked={force}
                  onChange={(e) => setForce(e.target.checked)}
                />
                <label className="form-check-label" htmlFor="force-reinit">
                  force (re-init even if already initialized)
                </label>
              </div>
            </div>
            <div className="col-12">
              <button type="submit" className="btn btn-primary" disabled={busy}>
                Create
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  )
}

function RetentionEditor({ onSave }: { onSave: (days: number) => void }) {
  const [days, setDays] = useState(30)
  return (
    <div className="input-group input-group-sm" style={{ width: '8rem' }}>
      <input
        type="number"
        className="form-control"
        value={days}
        onChange={(e) => setDays(Number(e.target.value))}
      />
      <button type="button" className="btn btn-outline-secondary" onClick={() => onSave(days)}>
        Set
      </button>
    </div>
  )
}
