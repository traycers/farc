import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { candidates, listStorages, setProtected, type StorageInfo } from '../api/farcd'
import { nsFromLocalInputValue, nsToLocalInputValue, type Candidate } from '../api/ns'

const ONE_HOUR_NS = 3_600_000_000_000n

export default function PlayerPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [channel, setChannel] = useState(1)
  const now = BigInt(Date.now()) * 1_000_000n
  const [from, setFrom] = useState(nsToLocalInputValue(now - ONE_HOUR_NS))
  const [to, setTo] = useState(nsToLocalInputValue(now))
  const [rows, setRows] = useState<Candidate[]>([])
  const [error, setError] = useState<string | null>(null)

  const videoRef = useRef<HTMLVideoElement | null>(null)
  const hlsRef = useRef<Hls | null>(null)

  useEffect(() => {
    listStorages()
      .then((s) => {
        setStorages(s)
        if (s.length > 0) setStorage((cur) => cur || s[0].id)
      })
      .catch((e) => setError(String(e)))
    return () => hlsRef.current?.destroy()
  }, [])

  async function onSearch(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    try {
      const t1 = nsFromLocalInputValue(from)
      const t2 = nsFromLocalInputValue(to)
      setRows(await candidates(storage, channel, t1, t2))
    } catch (e) {
      setError(String(e))
    }
  }

  async function onToggleProtected(c: Candidate, value: boolean) {
    setError(null)
    try {
      await setProtected(storage, c.uuid, value)
    } catch (e) {
      setError(String(e))
    }
  }

  function play(c: Candidate) {
    const url = `/api/hls/channels/${channel}/hls/${c.begin}/${c.end}/playlist.m3u8`
    const video = videoRef.current
    if (!video) return
    hlsRef.current?.destroy()
    if (Hls.isSupported()) {
      const hls = new Hls()
      hlsRef.current = hls
      hls.loadSource(url)
      hls.attachMedia(video)
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = url
    } else {
      setError('This browser supports neither MSE (hls.js) nor native HLS playback.')
    }
  }

  return (
    <section>
      <h1 className="mb-3">Player</h1>

      <div className="card mb-3">
        <div className="card-body">
          <form onSubmit={onSearch} className="row g-3 align-items-end">
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
            <div className="col-sm-6 col-md-2">
              <label className="form-label">
                channel id
                <input
                  type="number"
                  className="form-control"
                  value={channel}
                  onChange={(e) => setChannel(Number(e.target.value))}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-3">
              <label className="form-label">
                from
                <input
                  type="datetime-local"
                  className="form-control"
                  value={from}
                  onChange={(e) => setFrom(e.target.value)}
                />
              </label>
            </div>
            <div className="col-sm-6 col-md-3">
              <label className="form-label">
                to
                <input
                  type="datetime-local"
                  className="form-control"
                  value={to}
                  onChange={(e) => setTo(e.target.value)}
                />
              </label>
            </div>
            <div className="col-md-1">
              <button type="submit" className="btn btn-primary w-100">
                Search
              </button>
            </div>
          </form>
        </div>
      </div>

      {error && <div className="alert alert-danger">{error}</div>}

      <div className="table-responsive mb-3">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>uuid</th>
              <th>begin</th>
              <th>end</th>
              <th>protected</th>
              <th>play</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((c) => (
              <tr key={c.uuid}>
                <td>{c.uuid}</td>
                <td>{nsToLocalInputValue(c.begin)}</td>
                <td>{nsToLocalInputValue(c.end)}</td>
                <td>
                  <div className="btn-group btn-group-sm">
                    <button type="button" className="btn btn-outline-secondary" onClick={() => onToggleProtected(c, true)}>
                      set
                    </button>
                    <button type="button" className="btn btn-outline-secondary" onClick={() => onToggleProtected(c, false)}>
                      clear
                    </button>
                  </div>
                </td>
                <td>
                  <button type="button" className="btn btn-sm btn-primary" onClick={() => play(c)}>
                    play
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <video ref={videoRef} controls className="rounded" />
    </section>
  )
}
