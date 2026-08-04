import { useEffect, useState } from 'react'
import {
  createChannel,
  listChannels,
  listStorages,
  removeChannel,
  triggerEvent,
  updateChannel,
  type ChannelInfo,
  type StorageInfo,
} from '../api/farcd'

const POLICY_TYPES = ['continuous', 'event'] as const

export default function ChannelsPage() {
  const [storages, setStorages] = useState<StorageInfo[]>([])
  const [storage, setStorage] = useState('')
  const [channels, setChannels] = useState<ChannelInfo[]>([])
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)

  // null = "add" mode; a channel id = editing that channel.
  const [editingID, setEditingID] = useState<number | null>(null)
  const [id, setId] = useState(1)
  const [rtspURL, setRtspURL] = useState('')
  const [policyType, setPolicyType] = useState<(typeof POLICY_TYPES)[number]>('continuous')
  const [maxDeferredStartSec, setMaxDeferredStartSec] = useState(30)
  const [prerecordSec, setPrerecordSec] = useState(5)
  const [postrecordSec, setPostrecordSec] = useState(5)

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

  function resetForm() {
    setEditingID(null)
    setId((channels.reduce((max, c) => Math.max(max, c.channel), 0) || 0) + 1)
    setRtspURL('')
    setPolicyType('continuous')
    setMaxDeferredStartSec(30)
    setPrerecordSec(5)
    setPostrecordSec(5)
  }

  function startEdit(c: ChannelInfo) {
    setEditingID(c.channel)
    setId(c.channel)
    setRtspURL(c.rtsp_url)
    setPolicyType(c.capture_policy_type)
    setPrerecordSec(c.prerecord_ns / 1e9)
    setPostrecordSec(c.postrecord_ns / 1e9)
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setStatus(null)
    const capture_policy = {
      type: policyType,
      max_deferred_start_ns: Math.round(maxDeferredStartSec * 1e9),
      prerecord_ns: Math.round(prerecordSec * 1e9),
      postrecord_ns: Math.round(postrecordSec * 1e9),
    }
    try {
      if (editingID === null) {
        await createChannel({ id, rtsp_url: rtspURL, storage, capture_policy })
        setStatus(`channel ${id} created`)
      } else {
        await updateChannel(editingID, { rtsp_url: rtspURL, storage, capture_policy })
        setStatus(`channel ${editingID} updated`)
      }
      resetForm()
      refresh()
    } catch (e) {
      setError(String(e))
    }
  }

  async function onRemove(channel: number) {
    setError(null)
    setStatus(null)
    try {
      await removeChannel(channel)
      setStatus(`channel ${channel} removed`)
      if (editingID === channel) resetForm()
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

  const shown = channels.filter((c) => c.storage === storage)

  return (
    <section>
      <h1>Channels</h1>
      {error && <p className="error">{error}</p>}
      {status && <p>{status}</p>}

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

      <table>
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
                <button type="button" onClick={() => startEdit(c)}>
                  edit
                </button>
                <button type="button" onClick={() => onRemove(c.channel)}>
                  remove
                </button>
                <button type="button" onClick={() => onTrigger(c.channel)}>
                  trigger event
                </button>
              </td>
            </tr>
          ))}
          {shown.length === 0 && (
            <tr>
              <td colSpan={6}>no channels on this storage</td>
            </tr>
          )}
        </tbody>
      </table>

      <h2>{editingID === null ? 'Add channel' : `Edit channel ${editingID}`}</h2>
      <form onSubmit={onSubmit}>
        <label>
          id
          <input
            type="number"
            value={id}
            disabled={editingID !== null}
            onChange={(e) => setId(Number(e.target.value))}
          />
        </label>
        <label>
          rtsp_url
          <input
            value={rtspURL}
            onChange={(e) => setRtspURL(e.target.value)}
            placeholder="rtsp://camera1/stream"
            required
            style={{ width: '20rem' }}
          />
        </label>
        <label>
          capture policy
          <select value={policyType} onChange={(e) => setPolicyType(e.target.value as (typeof POLICY_TYPES)[number])}>
            {POLICY_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
        {policyType === 'continuous' ? (
          <label>
            max deferred start (seconds)
            <input
              type="number"
              value={maxDeferredStartSec}
              onChange={(e) => setMaxDeferredStartSec(Number(e.target.value))}
            />
          </label>
        ) : (
          <>
            <label>
              prerecord (seconds)
              <input type="number" value={prerecordSec} onChange={(e) => setPrerecordSec(Number(e.target.value))} />
            </label>
            <label>
              postrecord (seconds)
              <input type="number" value={postrecordSec} onChange={(e) => setPostrecordSec(Number(e.target.value))} />
            </label>
          </>
        )}
        <button type="submit">{editingID === null ? 'Add channel' : 'Save changes'}</button>
        {editingID !== null && (
          <button type="button" onClick={resetForm}>
            Cancel
          </button>
        )}
      </form>
    </section>
  )
}
