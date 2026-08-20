import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { createStorage } from '../../api/farcd'

const WRITE_MODES = ['cyclic', 'fill_until_full'] as const
const MiB = 1024 ** 2
const GiB = 1024 ** 3

// fchunk_size is a required POST /storages field (fblock.Params), but a
// low-level implementation detail (unit of physical write-with-verification,
// must be a multiple of the disk's cluster size) an operator has no reason
// to hand-tune -- treated the same way the total fblock count N already is
// below: computed/defaulted, never a raw form field.
const FCHUNK_SIZE = 4 * MiB

// GiB (1024-based), not GB (1e9-based), and consistently so everywhere size
// is computed or shown -- fblock sizes are themselves power-of-two byte
// counts, so as long as the desired size is a whole number of fblocks (the
// common case, e.g. 100 GiB / 1 GiB fblocks), N divides evenly and the
// result matches exactly what was asked for, with zero rounding.
function formatGiB(bytes: number): string {
  return (bytes / GiB).toFixed(2)
}

const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

// Auto-scaling 1024-based formatter, for showing what a percentage of a
// single fblock (typically MiB-GiB) resolves to in absolute terms --
// distinct from formatGiB above, which is fixed to GiB for whole-storage
// totals.
function formatBytes(bytes: number): string {
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < BYTE_UNITS.length - 1) {
    value /= 1024
    unitIndex++
  }
  return `${unitIndex === 0 ? value : value.toFixed(2)} ${BYTE_UNITS[unitIndex]}`
}

// crypto.randomUUID() would be simpler, but it requires a secure context
// (HTTPS or localhost) -- the docker-compose nginx deployment isn't
// guaranteed to have TLS, so this uses the unrestricted getRandomValues
// directly instead.
function generateStorageId(): string {
  const bytes = new Uint8Array(8)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

// Reuses the storage id (falling back to a fresh random one if it's still
// empty) so the file name on disk stays recognizable as belonging to that
// storage, rather than being an unrelated random path.
function generateStoragePath(id: string): string {
  return `/data/${id || generateStorageId()}.img`
}

function generateName(prefix: string): string {
  const bytes = new Uint8Array(4)
  crypto.getRandomValues(bytes)
  const suffix = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${prefix}-${suffix}`
}

export default function StorageNewPage() {
  const navigate = useNavigate()

  const [id, setId] = useState('')
  const [path, setPath] = useState('')
  const [name, setName] = useState('')
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
  // Pool size/warning/backpressure: how many fblocks this Storage's
  // write-buffer pool may hold in RAM at once (storage.PoolTuning) --
  // farcd's own default (storage.DefaultPoolTuning()) is 4/2/4, mirrored
  // here rather than left unset, since the UI is the only place this is
  // ever configured for a new storage.
  const [poolSize, setPoolSize] = useState(4)
  const [poolWarningAt, setPoolWarningAt] = useState(2)
  const [poolBackpressureAt, setPoolBackpressureAt] = useState(4)
  const [writeMode, setWriteMode] = useState<(typeof WRITE_MODES)[number]>('cyclic')
  const [retentionDays, setRetentionDays] = useState(30)
  // Shown/edited as a percentage (0-100) -- fblock.Params.MinContainerShare
  // itself is a [0,1] fraction; converted back on submit. 70 matches
  // fblock.DefaultMinContainerShare (the backend's own default), so an
  // untouched field submits exactly what farcd would otherwise default to.
  const [minContainerSharePercent, setMinContainerSharePercent] = useState(70)
  const [force, setForce] = useState(false)

  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onCreate(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await createStorage({
        id,
        path,
        name,
        geometry: { FblockSize: fblockSize, N: n, MaxChannels: maxChannels },
        params: {
          fchunk_size: FCHUNK_SIZE,
          write_mode: writeMode,
          retention: { days: retentionDays },
          min_container_share: minContainerSharePercent / 100,
        },
        force,
        catalog_path: '',
        backend: '',
        pool: { Size: poolSize, WarningAt: poolWarningAt, BackpressureAt: poolBackpressureAt },
      })
      navigate('/storages')
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">New storage</h1>
        <Link to="/storages" className="btn btn-outline-secondary">
          ← Back to storages
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      <div className="card">
        <div className="card-body">
          <p className="card-text text-body-secondary">
            For a plain file path, farcd creates and sizes the file itself (to <code>desired size</code> below,
            rounded down to a whole number of fblocks) — nothing to pre-create. For a raw block device/partition
            path, the partition must already be at least that size. Either way, farcd persists the new entry into
            its own config file on creation, so it survives a restart.
          </p>
          <form onSubmit={onCreate} className="d-flex flex-column gap-3">
            <div>
              <label className="form-label">
                id
                <div className="input-group">
                  <input className="form-control" value={id} onChange={(e) => setId(e.target.value)} required />
                  <button
                    type="button"
                    className="btn btn-outline-secondary"
                    onClick={() => setId(generateStorageId())}
                  >
                    Generate
                  </button>
                </div>
              </label>
            </div>
            <div>
              <label className="form-label">
                path
                <div className="input-group">
                  <input
                    className="form-control"
                    value={path}
                    onChange={(e) => setPath(e.target.value)}
                    placeholder="/data/disk0.img"
                    required
                  />
                  <button
                    type="button"
                    className="btn btn-outline-secondary"
                    onClick={() => setPath(generateStoragePath(id))}
                  >
                    Generate
                  </button>
                </div>
              </label>
            </div>
            <div>
              <label className="form-label">
                name
                <div className="input-group">
                  <input className="form-control" value={name} onChange={(e) => setName(e.target.value)} />
                  <button
                    type="button"
                    className="btn btn-outline-secondary"
                    onClick={() => setName(generateName('storage'))}
                  >
                    Generate
                  </button>
                </div>
              </label>
            </div>
            <div>
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
            <div>
              <label className="form-label">
                fblock size (MiB)
                <input
                  type="number"
                  className="form-control"
                  value={fblockSizeMiB}
                  onChange={(e) => setFblockSizeMiB(Number(e.target.value))}
                />
              </label>
              <div className="form-text">
                → N = {n} fblocks, actual size = {formatGiB(actualSizeBytes)} GiB
                {actualSizeBytes !== desiredSizeGiB * GiB &&
                  ' (not an exact multiple of the fblock size — rounded down)'}
              </div>
            </div>
            <div>
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
                ≈ {formatGiB(poolSize * fblockSize)} GiB RAM when the pool is full
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
            <div>
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
            <div>
              <label className="form-label">
                min container share (%)
                <input
                  type="number"
                  min="0"
                  max="100"
                  className="form-control"
                  value={minContainerSharePercent}
                  onChange={(e) => setMinContainerSharePercent(Number(e.target.value))}
                />
              </label>
              <div className="form-text">
                {formatBytes((fblockSize * minContainerSharePercent) / 100)} / {formatBytes(fblockSize)}
              </div>
            </div>
            <div>
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
            <div>
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
