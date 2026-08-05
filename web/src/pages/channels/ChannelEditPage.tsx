import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { listChannels, listStorages, updateChannel, type ChannelInfo, type StorageInfo } from '../../api/farcd'

const POLICY_TYPES = ['continuous', 'event'] as const

export default function ChannelEditPage() {
  const { id } = useParams<{ id: string }>()
  const channel = Number(id)
  const navigate = useNavigate()

  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [found, setFound] = useState<ChannelInfo | null | undefined>(undefined) // undefined = loading
  const [storage, setStorage] = useState('')
  const [name, setName] = useState('')
  const [rtspURL, setRtspURL] = useState('')
  const [policyType, setPolicyType] = useState<(typeof POLICY_TYPES)[number]>('continuous')
  const [maxDeferredStartSec, setMaxDeferredStartSec] = useState(30)
  const [prerecordSec, setPrerecordSec] = useState(5)
  const [postrecordSec, setPostrecordSec] = useState(5)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorages()
      .then(setStorages)
      .catch((e) => setError(String(e)))
    listChannels()
      .then((channels) => {
        const c = channels.find((c) => c.channel === channel) ?? null
        setFound(c)
        if (c) {
          setStorage(c.storage)
          setName(c.name ?? '')
          setRtspURL(c.rtsp_url)
          setPolicyType(c.capture_policy_type)
          setPrerecordSec(c.prerecord_ns / 1e9)
          setPostrecordSec(c.postrecord_ns / 1e9)
        }
      })
      .catch((e) => setError(String(e)))
  }, [channel])

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      await updateChannel(channel, {
        rtsp_url: rtspURL,
        storage,
        name,
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

  if (found === null) {
    return (
      <section>
        <h1 className="mb-3">Channel not found</h1>
        <p>No channel with id {id} exists.</p>
        <Link to="/channels" className="btn btn-outline-secondary">
          ← Back to channels
        </Link>
      </section>
    )
  }

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Edit channel {channel}</h1>
        <Link to="/channels" className="btn btn-outline-secondary">
          ← Back to channels
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}

      {found === undefined ? (
        <p className="text-body-secondary">Loading…</p>
      ) : (
        <div className="card">
          <div className="card-body">
            <form onSubmit={onSubmit} className="d-flex flex-column gap-3">
              <div>
                <label className="form-label">
                  id
                  <input type="number" className="form-control" value={channel} disabled />
                </label>
              </div>
              <div>
                <label className="form-label">
                  storage
                  <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
                    {storages.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name || s.id}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <div>
                <label className="form-label">
                  name
                  <input className="form-control" value={name} onChange={(e) => setName(e.target.value)} />
                </label>
              </div>
              <div>
                <label className="form-label">
                  rtsp_url
                  <input
                    className="form-control"
                    value={rtspURL}
                    onChange={(e) => setRtspURL(e.target.value)}
                    placeholder="rtsp://camera1/stream"
                    required
                  />
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
                <button type="submit" className="btn btn-primary">
                  Save changes
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </section>
  )
}
