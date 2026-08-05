import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { createChannel, listChannels, listStorages, type StorageInfo } from '../../api/farcd'

const POLICY_TYPES = ['continuous', 'event'] as const

export default function ChannelNewPage() {
  const navigate = useNavigate()

  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [id, setId] = useState(1)
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
        setStorage((cur) => cur || (s.length > 0 ? s[0].id : ''))
      })
      .catch((e) => setError(String(e)))
    listChannels()
      .then((channels) => setId((channels.reduce((max, c) => Math.max(max, c.channel), 0) || 0) + 1))
      .catch((e) => setError(String(e)))
  }, [])

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      await createChannel({
        id,
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
          <form onSubmit={onSubmit} className="row g-3 align-items-end">
            <div className="col-sm-6 col-md-3">
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
            <div className="col-sm-6 col-md-3">
              <label className="form-label">
                storage
                <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
                  {storages.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.id}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <div className="col-sm-6 col-md-6">
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
            <div className="col-sm-6 col-md-4">
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
              <div className="col-sm-6 col-md-4">
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
                <div className="col-sm-6 col-md-4">
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
                <div className="col-sm-6 col-md-4">
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
            <div className="col-12">
              <button type="submit" className="btn btn-primary">
                Add channel
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  )
}
