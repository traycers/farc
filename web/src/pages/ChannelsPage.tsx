import { useState } from 'react'
import { setCapturePolicy, triggerEvent } from '../api/farcd'

export default function ChannelsPage() {
  const [channel, setChannel] = useState(1)
  const [policyType, setPolicyType] = useState<'continuous' | 'event'>('continuous')
  const [prerecordSec, setPrerecordSec] = useState(5)
  const [postrecordSec, setPostrecordSec] = useState(5)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function onSetPolicy(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setStatus(null)
    try {
      await setCapturePolicy(channel, policyType, prerecordSec * 1e9, postrecordSec * 1e9)
      setStatus(`capture-policy set for channel ${channel}`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function onTrigger() {
    setError(null)
    setStatus(null)
    try {
      await triggerEvent(channel)
      setStatus(`event triggered for channel ${channel} (server timestamp)`)
    } catch (e) {
      setError(String(e))
    }
  }

  return (
    <section>
      <h1>Channels</h1>
      <p>
        farcd has no channel-listing API — channels only exist in its static config file. Enter a channel id you
        already know is configured; there is nothing to display for its <em>current</em> capture-policy either
        (only a setter exists), so this page is write-only by design.
      </p>

      <label>
        channel id
        <input type="number" value={channel} onChange={(e) => setChannel(Number(e.target.value))} />
      </label>

      <h2>Set capture policy</h2>
      <form onSubmit={onSetPolicy}>
        <label>
          type
          <select value={policyType} onChange={(e) => setPolicyType(e.target.value as 'continuous' | 'event')}>
            <option value="continuous">continuous</option>
            <option value="event">event</option>
          </select>
        </label>
        <label>
          prerecord (seconds)
          <input type="number" value={prerecordSec} onChange={(e) => setPrerecordSec(Number(e.target.value))} />
        </label>
        <label>
          postrecord (seconds)
          <input type="number" value={postrecordSec} onChange={(e) => setPostrecordSec(Number(e.target.value))} />
        </label>
        <button type="submit">Set policy</button>
      </form>

      <h2>Trigger event</h2>
      <p>Fires with the server's current wall-clock time — this admin UI does not backdate events.</p>
      <button type="button" onClick={onTrigger}>
        Trigger now
      </button>

      {status && <p>{status}</p>}
      {error && <p className="error">{error}</p>}
    </section>
  )
}
