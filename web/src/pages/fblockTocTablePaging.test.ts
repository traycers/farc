import { describe, expect, it } from 'vitest'
import { distinctValues, filterRows, tocRowsToCsv } from './fblockTocTablePaging'
import type { TocRow } from '../api/fblockTree'

function row(overrides: Partial<TocRow> = {}): TocRow {
  return { id: 0, type: 'void', role: 'root', parent_id: 0, sibling_id: 0, value_or_offset: '0', size: 0, ...overrides }
}

describe('tocRowsToCsv', () => {
  it('emits a tab-separated header row followed by one line per row', () => {
    const csv = tocRowsToCsv([
      row(),
      row({ id: 1, type: 'uint32', role: 'channel', parent_id: 0, sibling_id: 1, value_or_offset: '5', size: 0 }),
    ])
    const lines = csv.split('\n')
    expect(lines[0]).toBe('id\ttype\trole\tparent_id\tsibling_id\tvalue_or_offset\tsize')
    expect(lines[1]).toBe('0\tvoid\troot\t0\t0\t0\t0')
    expect(lines[2]).toBe('1\tuint32\tchannel\t0\t1\t5\t0')
  })

  it('prints value_or_offset verbatim as a string, never through numeric conversion', () => {
    // A real unix-ns timestamp exceeds JS's 2^53 safe-integer range --
    // if this were ever coerced through Number(), it would round.
    const csv = tocRowsToCsv([row({ value_or_offset: '1700000000123456789' })])
    expect(csv).toContain('1700000000123456789')
  })

  it('returns just the header for an empty row set', () => {
    expect(tocRowsToCsv([])).toBe('id\ttype\trole\tparent_id\tsibling_id\tvalue_or_offset\tsize')
  })
})

describe('distinctValues', () => {
  it('returns sorted unique values for the given field', () => {
    const rows = [row({ role: 'frame(video)' }), row({ role: 'channel' }), row({ role: 'frame(video)' })]
    expect(distinctValues(rows, 'role')).toEqual(['channel', 'frame(video)'])
  })
})

describe('filterRows', () => {
  const rows = [
    row({ id: 0, role: 'root', type: 'void' }),
    row({ id: 1, role: 'channel', type: 'uint32', value_or_offset: '7' }),
    row({ id: 2, role: 'frame(video)', type: 'bytes' }),
  ]

  it('with no filter set, returns every row unchanged', () => {
    expect(filterRows(rows, { search: '', role: '', type: '' })).toEqual(rows)
  })

  it('filters by exact role match', () => {
    expect(filterRows(rows, { search: '', role: 'channel', type: '' })).toEqual([rows[1]])
  })

  it('filters by exact type match', () => {
    expect(filterRows(rows, { search: '', role: '', type: 'bytes' })).toEqual([rows[2]])
  })

  it('filters by a case-insensitive text search across every field', () => {
    expect(filterRows(rows, { search: 'FRAME(VIDEO)', role: '', type: '' })).toEqual([rows[2]])
  })
})
