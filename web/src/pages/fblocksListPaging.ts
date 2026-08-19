import type { CatalogEntry } from '../api/fblockTree'

// visibleEntries applies FblocksListPage's state filter -- purely a display
// concern, the full catalog is already fetched in one request regardless
// (settled via grilling, 2026-08-14: no server-side filtering needed).
export function visibleEntries(entries: CatalogEntry[], hideUninitialized: boolean): CatalogEntry[] {
  return hideUninitialized ? entries.filter((e) => e.state !== 'uninitialized') : entries
}

// pageOf slices items into 0-based page of size pageSize -- pagination is
// purely client-side (settled via grilling): the whole catalog is already
// in memory, so this just bounds how many rows render at once.
export function pageOf<T>(items: T[], page: number, pageSize: number): T[] {
  const start = page * pageSize
  return items.slice(start, start + pageSize)
}

// totalPages is always at least 1, even for zero items, so page 0 is
// always a valid page to be on.
export function totalPages(count: number, pageSize: number): number {
  return Math.max(1, Math.ceil(count / pageSize))
}
