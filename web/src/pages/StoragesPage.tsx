import { useEffect, useState } from 'react'
import { createStorage, listStorages, patchStorage, type StorageInfo } from '../api/farcd'

const WRITE_MODES = ['cyclic', 'fill_until_full'] as const

export default function StoragesPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const [id, setId] = useState('')
  const [path, setPath] = useState('')
  const [fblockSize, setFblockSize] = useState(1024 * 1024 * 1024)
  const [n, setN] = useState(16)
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
      <h1>Storages</h1>
      {error && <p className="error">{error}</p>}

      <table>
        <thead>
          <tr>
            <th>id</th>
            <th>path</th>
            <th>fblock size</th>
            <th>N</th>
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
              <td>{s.geometry.FblockSize}</td>
              <td>{s.geometry.N}</td>
              <td>{s.geometry.MaxChannels}</td>
              <td>
                <RetentionEditor onSave={(days) => onPatch(s.id, { retention_days: days })} />
              </td>
              <td>
                <select onChange={(e) => onPatch(s.id, { write_mode: e.target.value })} defaultValue="">
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

      <h2>Create storage</h2>
      <p>
        Registration is in-memory for the running farcd process only — it is <em>not</em> written back into
        farcd's config file. To survive a container restart, add the same <code>id</code>/<code>path</code> to
        the mounted <code>farc.config.json</code> and restart <code>farc</code> (see PLAN.md, Gap 3).
      </p>
      <form onSubmit={onCreate}>
        <label>
          id
          <input value={id} onChange={(e) => setId(e.target.value)} required />
        </label>
        <label>
          path
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/data/disk0.img" required />
        </label>
        <label>
          fblock size (bytes)
          <input type="number" value={fblockSize} onChange={(e) => setFblockSize(Number(e.target.value))} />
        </label>
        <label>
          N (fblock count)
          <input type="number" value={n} onChange={(e) => setN(Number(e.target.value))} />
        </label>
        <label>
          max channels
          <input type="number" value={maxChannels} onChange={(e) => setMaxChannels(Number(e.target.value))} />
        </label>
        <label>
          fchunk size (bytes)
          <input type="number" value={fchunkSize} onChange={(e) => setFchunkSize(Number(e.target.value))} />
        </label>
        <label>
          write mode
          <select value={writeMode} onChange={(e) => setWriteMode(e.target.value as (typeof WRITE_MODES)[number])}>
            {WRITE_MODES.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </label>
        <label>
          retention (days)
          <input type="number" value={retentionDays} onChange={(e) => setRetentionDays(Number(e.target.value))} />
        </label>
        <label>
          min container share
          <input
            type="number"
            step="0.01"
            value={minContainerShare}
            onChange={(e) => setMinContainerShare(Number(e.target.value))}
          />
        </label>
        <label>
          force (re-init even if already initialized)
          <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
        </label>
        <button type="submit" disabled={busy}>
          Create
        </button>
      </form>
    </section>
  )
}

function RetentionEditor({ onSave }: { onSave: (days: number) => void }) {
  const [days, setDays] = useState(30)
  return (
    <span style={{ display: 'flex', gap: '0.25rem' }}>
      <input type="number" value={days} onChange={(e) => setDays(Number(e.target.value))} style={{ width: '4rem' }} />
      <button type="button" onClick={() => onSave(days)}>
        Set
      </button>
    </span>
  )
}
