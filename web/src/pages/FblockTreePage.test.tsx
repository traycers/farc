import { describe, expect, it } from 'vitest'
import { formatValue } from './FblockTreePage'
import type { TreeNode } from '../api/fblockTree'

function node(role: string, type: string, value: string): TreeNode {
  return { id: 1, parent_id: 0, type, role, value }
}

describe('FblockTreePage formatValue', () => {
  it('decodes a codec(video) node to its codec name', () => {
    expect(formatValue(node('codec(video)', 'uint8', '2'))).toBe('H265')
  })

  it('decodes a frame_kind node to I/P', () => {
    expect(formatValue(node('frame_kind', 'uint8', '73'))).toBe('I')
  })

  it('leaves an unrelated uint8 node value undefined (falls back to raw node.value)', () => {
    expect(formatValue(node('sample_rate', 'uint32', '48000'))).toBeUndefined()
  })

  it('formats a timestamp node with microsecond precision, distinguishing adjacent frames', () => {
    const a = formatValue(node('frame_time(video)', 'timestamp', '1700000000000001000'))
    const b = formatValue(node('frame_time(video)', 'timestamp', '1700000000000002000'))
    expect(a).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}$/)
    expect(a).not.toBe(b)
  })
})
