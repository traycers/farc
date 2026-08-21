import type { TocRow } from '../api/fblockTree'

const CSV_HEADER = ['id', 'type', 'role', 'parent_id', 'sibling_id', 'value_or_offset', 'size']

// tocRowsToCsv serializes rows to tab-separated text with a header row.
// Callers always pass the FULL, unfiltered row set (settled via grilling,
// 2026-08-21: the CSV is a full dump, independent of whatever role/type
// filter is active in the table UI) -- this function itself has no
// filtering concept at all. value_or_offset is printed verbatim (it's
// already a string on TocRow, never coerced through Number()).
export function tocRowsToCsv(rows: TocRow[]): string {
  const lines = rows.map((r) => [r.id, r.type, r.role, r.parent_id, r.sibling_id, r.value_or_offset, r.size].join('\t'))
  return [CSV_HEADER.join('\t'), ...lines].join('\n')
}

// distinctValues powers the table's role/type filter dropdowns -- built
// from whatever rows were actually fetched, not a fixed enum list.
export function distinctValues(rows: TocRow[], field: 'role' | 'type'): string[] {
  return Array.from(new Set(rows.map((r) => r[field]))).sort()
}

export type TocRowFilter = {
  search: string
  role: string // '' = any
  type: string // '' = any
}

// filterRows applies the table's search/role/type filter -- purely a
// display concern (settled via grilling, 2026-08-21): the fetched row
// array itself is never mutated, and tocRowsToCsv's caller must pass the
// UNFILTERED array, never this function's result.
export function filterRows(rows: TocRow[], filter: TocRowFilter): TocRow[] {
  const search = filter.search.trim().toLowerCase()
  return rows.filter((r) => {
    if (filter.role && r.role !== filter.role) return false
    if (filter.type && r.type !== filter.type) return false
    if (search) {
      const haystack = `${r.id} ${r.type} ${r.role} ${r.parent_id} ${r.sibling_id} ${r.value_or_offset} ${r.size}`.toLowerCase()
      if (!haystack.includes(search)) return false
    }
    return true
  })
}
