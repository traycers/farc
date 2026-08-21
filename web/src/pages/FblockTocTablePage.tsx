import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useVirtualizer } from '@tanstack/react-virtual'
import { getFblockInfo, getFblockTocRows, subscribeFblockLiveTocRows, type FblockInfo, type TocRow } from '../api/fblockTree'
import { downloadTextFile } from '../lib/download'
import { distinctValues, filterRows, tocRowsToCsv, type TocRowFilter } from './fblockTocTablePaging'

const ROW_HEIGHT = 28

// FblockTocTablePage shows the same TOC data FblockTreePage's tree renders,
// as a flat, filterable, virtualized table instead -- reached via that
// page's "TOC table" link. Rows are the raw SoA-shaped shape the backend
// sends (id/type/role/parent_id/sibling_id/value_or_offset/size), no
// depth/derived columns: the table is an exact mirror of the CSV export
// (settled via grilling, 2026-08-21), not a display-optimized view.
export default function FblockTocTablePage() {
  const { id = '', index = '' } = useParams<{ id: string; index: string }>()
  const [info, setInfo] = useState<FblockInfo | null>(null)
  const [rows, setRows] = useState<TocRow[]>([])
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<TocRowFilter>({ search: '', role: '', type: '' })
  const parentRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setError(null)
    setInfo(null)
    setRows([])
    getFblockInfo(id, Number(index))
      .then((i) => {
        setInfo(i)
        if (i.state === 'ready') {
          if (!i.uuid) throw new Error(`fblock ${index} is ready but has no uuid`)
          return getFblockTocRows(id, i.uuid).then(setRows)
        }
        if (i.state === 'in_progress') {
          return undefined // live subscription effect below handles this
        }
        throw new Error(`fblock ${index} is not viewable (state=${i.state})`)
      })
      .catch((e) => setError(String(e)))
  }, [id, index])

  useEffect(() => {
    if (info?.state !== 'in_progress') return
    setRows([])
    return subscribeFblockLiveTocRows(id, Number(index), (msg) => setRows(msg.rows ?? []))
  }, [id, index, info?.state])

  // filtered is display-only -- tocRowsToCsv below always takes `rows`
  // (the full, unfiltered fetch), never `filtered` (spec decision 9).
  const filtered = useMemo(() => filterRows(rows, filter), [rows, filter])
  const roles = useMemo(() => distinctValues(rows, 'role'), [rows])
  const types = useMemo(() => distinctValues(rows, 'type'), [rows])

  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
  })

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">
          TOC table: fblock #{index} <small className="text-body-secondary">({id})</small>
        </h1>
        <button
          type="button"
          className="btn btn-outline-secondary"
          onClick={() => downloadTextFile(`fblock-${info?.uuid ?? index}-toc.csv`, tocRowsToCsv(rows), 'text/csv')}
        >
          Download TOC (.csv)
        </button>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}
      {info && (
        <div className="mb-3 d-flex gap-3 align-items-center">
          <span className="badge text-bg-secondary">{info.state}</span>
          {info.protected && <span className="badge text-bg-info">protected</span>}
          {info.uuid && <span>uuid: {info.uuid}</span>}
        </div>
      )}
      <div className="d-flex gap-2 mb-3">
        <label className="flex-grow-1">
          search
          <input
            className="form-control"
            value={filter.search}
            onChange={(e) => setFilter((f) => ({ ...f, search: e.target.value }))}
          />
        </label>
        <label>
          role
          <select className="form-select" value={filter.role} onChange={(e) => setFilter((f) => ({ ...f, role: e.target.value }))}>
            <option value="">any</option>
            {roles.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
        <label>
          type
          <select className="form-select" value={filter.type} onChange={(e) => setFilter((f) => ({ ...f, type: e.target.value }))}>
            <option value="">any</option>
            {types.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div className="d-flex fw-semibold border-bottom py-1 px-2 gap-3 font-monospace">
        <span style={{ width: '4rem' }}>id</span>
        <span style={{ width: '6rem' }}>type</span>
        <span style={{ width: '14rem' }}>role</span>
        <span style={{ width: '6rem' }}>parent_id</span>
        <span style={{ width: '6rem' }}>sibling_id</span>
        <span style={{ width: '14rem' }}>value_or_offset</span>
        <span style={{ width: '6rem' }}>size</span>
      </div>
      <div ref={parentRef} className="border rounded font-monospace" style={{ height: '32rem', overflow: 'auto' }} data-testid="toc-rows">
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((vi) => {
            const row = filtered[vi.index]
            return (
              <div
                key={row.id}
                className="d-flex gap-3 px-2 border-bottom"
                style={{ position: 'absolute', top: 0, left: 0, width: '100%', height: ROW_HEIGHT, transform: `translateY(${vi.start}px)` }}
              >
                <span style={{ width: '4rem' }}>{row.id}</span>
                <span style={{ width: '6rem' }}>{row.type}</span>
                <span style={{ width: '14rem' }}>{row.role}</span>
                <span style={{ width: '6rem' }}>{row.parent_id}</span>
                <span style={{ width: '6rem' }}>{row.sibling_id}</span>
                <span style={{ width: '14rem' }}>{row.value_or_offset}</span>
                <span style={{ width: '6rem' }}>{row.size}</span>
              </div>
            )
          })}
        </div>
      </div>
      {filtered.length === 0 && !error && <div className="text-body-secondary mt-2">no rows</div>}
    </section>
  )
}
