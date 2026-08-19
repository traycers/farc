import { describe, expect, it } from 'vitest'
import { pageOf, totalPages, visibleEntries } from './fblocksListPaging'
import type { CatalogEntry } from '../api/fblockTree'

function entry(index: number, state: string): CatalogEntry {
  return { index, state }
}

describe('visibleEntries', () => {
  it('hides uninitialized entries when hideUninitialized is true', () => {
    const entries = [entry(0, 'ready'), entry(1, 'uninitialized'), entry(2, 'in_progress')]
    expect(visibleEntries(entries, true).map((e) => e.index)).toEqual([0, 2])
  })

  it('keeps every entry when hideUninitialized is false', () => {
    const entries = [entry(0, 'ready'), entry(1, 'uninitialized')]
    expect(visibleEntries(entries, false).map((e) => e.index)).toEqual([0, 1])
  })
})

describe('pageOf', () => {
  const items = Array.from({ length: 25 }, (_, i) => i)

  it('returns the first pageSize items for page 0', () => {
    expect(pageOf(items, 0, 10)).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9])
  })

  it('returns the next slice for page 1', () => {
    expect(pageOf(items, 1, 10)).toEqual([10, 11, 12, 13, 14, 15, 16, 17, 18, 19])
  })

  it('returns a partial final page', () => {
    expect(pageOf(items, 2, 10)).toEqual([20, 21, 22, 23, 24])
  })
})

describe('totalPages', () => {
  it('computes the number of pages, rounding up', () => {
    expect(totalPages(25, 10)).toBe(3)
    expect(totalPages(20, 10)).toBe(2)
  })

  it('is at least 1 even for zero items', () => {
    expect(totalPages(0, 10)).toBe(1)
  })
})
