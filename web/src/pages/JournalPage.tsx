import { useEffect, useState } from 'react'
import { subscribeJournal, type JournalEvent } from '../api/events'

const MAX_EVENTS = 500

type LoggedEvent = JournalEvent & { receivedAt: string }

export default function JournalPage() {
  const [connected, setConnected] = useState(false)
  const [events, setEvents] = useState<LoggedEvent[]>([])

  useEffect(() => {
    const unsubscribe = subscribeJournal(
      (e) => {
        setEvents((prev) => [{ ...e, receivedAt: new Date().toLocaleTimeString() }, ...prev].slice(0, MAX_EVENTS))
      },
      setConnected,
    )
    return unsubscribe
  }, [])

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Журнал</h1>
        <div className="d-flex align-items-center gap-3">
          <span className={`badge ${connected ? 'text-bg-success' : 'text-bg-warning'}`}>
            {connected ? 'connected' : 'reconnecting…'}
          </span>
          <button type="button" className="btn btn-outline-secondary btn-sm" onClick={() => setEvents([])}>
            Clear
          </button>
        </div>
      </div>

      <div className="table-responsive">
        <table className="table table-striped table-hover align-middle">
          <thead>
            <tr>
              <th>time</th>
              <th>event</th>
              <th>channel</th>
              <th>storage</th>
              <th>index</th>
              <th>uuid</th>
              <th>reason</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e, i) => (
              <tr key={i}>
                <td>{e.receivedAt}</td>
                <td>{e.name}</td>
                <td>{e.channel ?? ''}</td>
                <td>{e.storage ?? ''}</td>
                <td>{e.index ?? ''}</td>
                <td>{e.uuid ?? ''}</td>
                <td>{e.reason ?? ''}</td>
              </tr>
            ))}
            {events.length === 0 && (
              <tr>
                <td colSpan={7} className="text-body-secondary">
                  no events yet
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
