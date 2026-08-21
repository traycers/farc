import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { listChannels, listStorages, type ChannelInfo, type StorageInfo } from '../../api/farcd'
import { createChannel } from '../../api/apid'

const POLICY_TYPES = ['continuous', 'event'] as const

function generateName(prefix: string): string {
  const bytes = new Uint8Array(4)
  crypto.getRandomValues(bytes)
  const suffix = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${prefix}-${suffix}`
}

export default function ChannelNewPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const requestedStorage = searchParams.get('storage') ?? ''

  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [storage, setStorage] = useState(requestedStorage)
  const [id, setId] = useState(1)
  const [name, setName] = useState('')
  const [rtspURL, setRtspURL] = useState('')
  const [policyType, setPolicyType] = useState<(typeof POLICY_TYPES)[number]>('continuous')
  const [maxDeferredStartSec, setMaxDeferredStartSec] = useState(30)
  const [prerecordSec, setPrerecordSec] = useState(5)
  const [postrecordSec, setPostrecordSec] = useState(5)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then((s) => {
        setStorages(s)
        setStorage((cur) => (s.some((st) => st.id === cur) ? cur : s.length > 0 ? s[0].id : ''))
      })
      .catch((e) => setError(String(e)))
    listChannels()
      .then((c) => {
        setChannels(c)
        setId((c.reduce((max, ch) => Math.max(max, ch.channel), 0) || 0) + 1)
      })
      .catch((e) => setError(String(e)))
  }, [])

  function isFull(storageID: string): boolean {
    const s = storages.find((st) => st.id === storageID)
    return s !== undefined && channels.filter((c) => c.storage === storageID).length >= s.geometry.MaxChannels
  }

  const selectedStorageFull = isFull(storage)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      await createChannel({
        id,
        name,
        rtsp_url: rtspURL,
        storage,
        capture_policy: {
          type: policyType,
          max_deferred_start_ns: Math.round(maxDeferredStartSec * 1e9),
          prerecord_ns: Math.round(prerecordSec * 1e9),
          postrecord_ns: Math.round(postrecordSec * 1e9),
        },
      })
      navigate('/channels')
    } catch (e) {
      setError(String(e))
    }
  }

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">New channel</h1>
        <Link to="/channels" className="btn btn-outline-secondary">
          ← Back to channels
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      <div className="card">
        <div className="card-body">
          <form onSubmit={onSubmit} className="d-flex flex-column gap-3">
            <div>
              <label className="form-label">
                id
                <input
                  type="number"
                  className="form-control"
                  value={id}
                  onChange={(e) => setId(Number(e.target.value))}
                />
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
                    onClick={() => setName(generateName('channel'))}
                  >
                    Generate
                  </button>
                </div>
              </label>
            </div>
            <div>
              <label className="form-label">
                storage
                <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
                  {storages.map((s) => {
                    const full = isFull(s.id)
                    const count = channels.filter((c) => c.storage === s.id).length
                    return (
                      <option key={s.id} value={s.id} disabled={full}>
                        {s.name || s.id}
                        {full ? ` (full, ${count}/${s.geometry.MaxChannels})` : ''}
                      </option>
                    )
                  })}
                </select>
              </label>
            </div>
            <div>
              <label className="form-label">
                rtsp_url
                <div className="input-group">
                  <input
                    className="form-control"
                    value={rtspURL}
                    onChange={(e) => setRtspURL(e.target.value)}
                    placeholder="rtsp://camera1/stream"
                    required
                  />
                  <button
                    type="button"
                    className="btn btn-outline-secondary"
                    data-testid="rtsp-url-generate-btn"
                    onClick={() => setRtspURL('rtsp://mediamtx:8554/test')}
                  >
                    Generate
                  </button>
                </div>
              </label>
            </div>
            <div>
              <label className="form-label">
                capture policy
                <select
                  className="form-select"
                  value={policyType}
                  onChange={(e) => setPolicyType(e.target.value as (typeof POLICY_TYPES)[number])}
                >
                  {POLICY_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {policyType === 'continuous' ? (
              <div>
                <label className="form-label">
                  max deferred start (seconds)
                  <input
                    type="number"
                    className="form-control"
                    value={maxDeferredStartSec}
                    onChange={(e) => setMaxDeferredStartSec(Number(e.target.value))}
                  />
                </label>
              </div>
            ) : (
              <>
                <div>
                  <label className="form-label">
                    prerecord (seconds)
                    <input
                      type="number"
                      className="form-control"
                      value={prerecordSec}
                      onChange={(e) => setPrerecordSec(Number(e.target.value))}
                    />
                  </label>
                </div>
                <div>
                  <label className="form-label">
                    postrecord (seconds)
                    <input
                      type="number"
                      className="form-control"
                      value={postrecordSec}
                      onChange={(e) => setPostrecordSec(Number(e.target.value))}
                    />
                  </label>
                </div>
              </>
            )}
            <div>
              <button type="submit" className="btn btn-primary" disabled={selectedStorageFull}>
                Add channel
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  )
}
