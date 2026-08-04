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
      <h1>Player</h1>

      <form onSubmit={onSearch}>
        <label>
          storage
          <select value={storage} onChange={(e) => setStorage(e.target.value)}>
            {storages.map((s) => (
              <option key={s.id} value={s.id}>
                {s.id}
              </option>
            ))}
          </select>
        </label>
        <label>
          channel id
          <input type="number" value={channel} onChange={(e) => setChannel(Number(e.target.value))} />
        </label>
        <label>
          from
          <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label>
          to
          <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
        </label>
        <button type="submit">Search candidates</button>
      </form>

      {error && <p className="error">{error}</p>}

      <table>
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
                <button type="button" onClick={() => onToggleProtected(c, true)}>
                  set
                </button>
                <button type="button" onClick={() => onToggleProtected(c, false)}>
                  clear
                </button>
              </td>
              <td>
                <button type="button" onClick={() => play(c)}>
                  play
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <video ref={videoRef} controls />
    </section>
  )
}
