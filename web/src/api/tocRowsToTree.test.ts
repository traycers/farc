import { describe, expect, it } from 'vitest'
import { tocRowsToTree } from './tocRowsToTree'
import type { TocRow } from './fblockTree'

function row(overrides: Partial<TocRow>): TocRow {
  return { id: 0, type: 'void', role: 'root', parent_id: 0, sibling_id: 0, value_or_offset: '0', size: 0, ...overrides }
}

describe('tocRowsToTree', () => {
  it('returns null for an empty row set', () => {
    expect(tocRowsToTree([])).toBeNull()
  })

  it('finds the root via the self-reference convention (id === parent_id), not array position', () => {
    // root is NOT first in this array -- the function must not assume position 0.
    const rows = [
      row({ id: 1, type: 'uint32', role: 'channel', parent_id: 0, value_or_offset: '5' }),
      row({ id: 0, type: 'void', role: 'root', parent_id: 0 }),
    ]
    const tree = tocRowsToTree(rows)
    expect(tree?.id).toBe(0)
    expect(tree?.role).toBe('root')
  })

  it('nests children under their parent, in row order, and branches value vs size by type', () => {
    const rows: TocRow[] = [
      row({ id: 0, type: 'void', role: 'root', parent_id: 0 }),
      row({ id: 1, type: 'uint32', role: 'channel', parent_id: 0, value_or_offset: '5' }),
      row({ id: 2, type: 'bytes', role: 'frame_data(video)', parent_id: 0, value_or_offset: '0', size: 3 }),
    ]
    const tree = tocRowsToTree(rows)

    expect(tree).toEqual({
      id: 0,
      parent_id: 0,
      type: 'void',
      role: 'root',
      children: [
        { id: 1, parent_id: 0, type: 'uint32', role: 'channel', value: '5' },
        { id: 2, parent_id: 0, type: 'bytes', role: 'frame_data(video)', size: 3 },
      ],
    })
  })

  it('nests grandchildren correctly (multi-level tree)', () => {
    const rows: TocRow[] = [
      row({ id: 0, type: 'void', role: 'root', parent_id: 0 }),
      row({ id: 1, type: 'void', role: 'channels', parent_id: 0 }),
      row({ id: 2, type: 'uint32', role: 'channel', parent_id: 1, value_or_offset: '7' }),
    ]
    const tree = tocRowsToTree(rows)
    expect(tree?.children?.[0].children?.[0]).toEqual({ id: 2, parent_id: 1, type: 'uint32', role: 'channel', value: '7' })
  })
})
