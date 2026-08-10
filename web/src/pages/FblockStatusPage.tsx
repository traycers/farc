import { Link, useSearchParams } from 'react-router-dom'
import FblockTree from '../components/FblockTree'

// Detail-only view for one specific fblock -- reached via FblockListPage's
// "Open" button or FblockLivePage's "предыдущий fblock →" links, never a
// standalone entry point of its own (no storage/uuid picker here).
export default function FblockStatusPage() {
  const [searchParams] = useSearchParams()
  const storage = searchParams.get('storage') ?? ''
  const uuid = searchParams.get('uuid') ?? ''

  if (!storage || !uuid) {
    return (
      <section>
        <h1 className="mb-3">Fblock (status)</h1>
        <p>
          Выберите фблок в <Link to="/fblocks">списке фблоков</Link>.
        </p>
      </section>
    )
  }

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">Fblock (status)</h1>
        <Link to={`/fblocks?storage=${encodeURIComponent(storage)}`}>← к списку фблоков</Link>
      </div>

      <FblockTree mode="status" storage={storage} uuid={uuid} />
    </section>
  )
}
