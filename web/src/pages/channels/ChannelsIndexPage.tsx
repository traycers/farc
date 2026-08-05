import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  listChannels,
  listStorages,
  removeChannel,
  startRecording,
  stopRecording,
  triggerEvent,
  type ChannelInfo,
  type StorageInfo,
} from '../../api/farcd'

export default function ChannelsIndexPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)

  const refresh = () => {
    listStorages()
      .then((s) => {
        setStorages(s)
        setStorage((cur) => cur || (s.length > 0 ? s[0].id : ''))
      })
      .catch((e) => setError(String(e)))
    listChannels()
      .then(setChannels)
      .catch((e) => setError(String(e)))
  }

  useEffect(() => {
    refresh()
  }, [])

  async function onRemove(channel: number) {
    setError(null)
    setStatus(null)
    try {
      await removeChannel(channel)
      setStatus(`channel ${channel} removed`)
      refresh()
    } catch (e) {
      setError(String(e))
    }
  }

  async function onTrigger(channel: number) {
    setError(null)
    setStatus(null)
    try {
      await triggerEvent(channel)
      setStatus(`event triggered for channel ${channel}`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onStartRecording(channel: number) {
    setError(null)
    setStatus(null)
    try {
      await startRecording(channel)
      setStatus(`recording started for channel ${channel}`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onStopRecording(channel: number) {
    setError(null)
    setStatus(null)
    try {
      await stopRecording(channel)
      setStatus(`recording stopped for channel ${channel}`)
    } catch (e) {
      setError(String(e))
    }
  }

  const shown = channels.filter((c) => c.storage === storage)

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Channels</h1>
        <Link to="new" className="btn btn-primary">
          New channel
        </Link>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}
      {status && <div className="alert alert-success">{status}</div>}

      <label className="d-block mb-3" style={{ maxWidth: '24rem' }}>
        storage
        <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
          {storages.map((s) => (
            <option key={s.id} value={s.id}>
              {s.id}
            </option>
          ))}
        </select>
      </label>

      <div className="table-responsive">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>id</th>
              <th>rtsp_url</th>
              <th>policy</th>
              <th>prerecord</th>
              <th>postrecord</th>
              <th>actions</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((c) => (
              <tr key={c.channel}>
                <td>{c.channel}</td>
                <td>{c.rtsp_url}</td>
                <td>{c.capture_policy_type}</td>
                <td>{c.prerecord_ns / 1e9}s</td>
                <td>{c.postrecord_ns / 1e9}s</td>
                <td>
                  <div className="btn-group btn-group-sm">
                    <Link to={`${c.channel}/edit`} className="btn btn-outline-secondary">
                      edit
                    </Link>
                    <button type="button" className="btn btn-outline-danger" onClick={() => onRemove(c.channel)}>
                      remove
                    </button>
                    {c.capture_policy_type === 'continuous' ? (
                      <>
                        <button
                          type="button"
                          className="btn btn-outline-success"
                          onClick={() => onStartRecording(c.channel)}
                        >
                          start recording
                        </button>
                        <button
                          type="button"
                          className="btn btn-outline-warning"
                          onClick={() => onStopRecording(c.channel)}
                        >
                          stop recording
                        </button>
                      </>
                    ) : (
                      <button type="button" className="btn btn-outline-primary" onClick={() => onTrigger(c.channel)}>
                        trigger event
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
            {shown.length === 0 && (
              <tr>
                <td colSpan={6} className="text-body-secondary">
                  no channels on this storage
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
