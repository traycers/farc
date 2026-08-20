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
import { subscribeJournal } from '../../api/events'

export default function ChannelsIndexPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [connectFailedBanner, setConnectFailedBanner] = useState<string | null>(null)

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

  // Live status updates (.scratch/web-ui-fixes/issues/03, 04): the initial
  // recording/last_connect_error values above come from GET /channels, this
  // keeps them current without a refetch for as long as the page stays
  // open. subscribeJournal's own no-catch-up policy means a gap while
  // disconnected is simply lost -- refresh() above (on mount) is this
  // page's only resync point, matching every other page's convention.
  useEffect(() => {
    return subscribeJournal((e) => {
      if (e.channel === undefined) return
      if (e.name === 'channel.recording.started' || e.name === 'channel.recording.stopped') {
        const recording = e.name === 'channel.recording.started'
        setChannels((prev) => prev.map((c) => (c.channel === e.channel ? { ...c, recording } : c)))
        return
      }
      if (e.name === 'channel.rtsp.connect_failed') {
        setConnectFailedBanner(`Channel ${e.channel}: ${e.reason ?? ''}`)
        setChannels((prev) => prev.map((c) => (c.channel === e.channel ? { ...c, last_connect_error: e.reason } : c)))
        return
      }
      if (e.name === 'channel.rtsp.connected') {
        setChannels((prev) => prev.map((c) => (c.channel === e.channel ? { ...c, last_connect_error: undefined } : c)))
      }
    })
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
      {connectFailedBanner && <div className="alert alert-danger">{connectFailedBanner}</div>}
      {status && <div className="alert alert-success">{status}</div>}

      <label className="d-block mb-3" style={{ maxWidth: '24rem' }}>
        storage
        <select className="form-select" value={storage} onChange={(e) => setStorage(e.target.value)}>
          {storages.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name || s.id}
            </option>
          ))}
        </select>
      </label>

      <div className="table-responsive">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>id</th>
              <th>name</th>
              <th>status</th>
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
                <td>{c.name}</td>
                <td>
                  <span
                    data-testid={`channel-recording-dot-${c.channel}`}
                    className={`status-dot ${c.recording ? 'status-dot-recording' : 'status-dot-idle'}`}
                    title={c.recording ? 'recording' : 'not recording'}
                  />
                  {c.last_connect_error && (
                    <div className="text-danger small" data-testid={`channel-connect-error-${c.channel}`}>
                      {c.last_connect_error}
                    </div>
                  )}
                </td>
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
                <td colSpan={7} className="text-body-secondary">
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
