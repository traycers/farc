import { describe, expect, it } from 'vitest'
import { renderTreeAsText } from './fblockTreeText'
import type { TreeNode } from '../api/fblockTree'

function tree(): TreeNode {
  return {
    id: 0,
    parent_id: 0,
    type: 'void',
    role: 'root',
    children: [
      { id: 1, parent_id: 0, type: 'uint8', role: 'a', value: '1' },
      {
        id: 2,
        parent_id: 0,
        type: 'uint8',
        role: 'b',
        value: '2',
        children: [{ id: 3, parent_id: 2, type: 'uint8', role: 'c', value: '3' }],
      },
    ],
  }
}

function deepChain(): TreeNode {
  let node: TreeNode = { id: 6, parent_id: 5, type: 'void', role: 'n6' }
  for (let depth = 5; depth >= 0; depth--) {
    node = { id: depth, parent_id: Math.max(depth - 1, 0), type: 'void', role: `n${depth}`, children: [node] }
  }
  return node
}

describe('renderTreeAsText', () => {
  it('renders the root with no connector and children with GNU-tree connectors', () => {
    const lines = renderTreeAsText(tree()).split('\n')
    expect(lines[0]).toBe('root (void)')
    expect(lines[1]).toBe('├── a (uint8) = 1')
    expect(lines[2]).toBe('└── b (uint8) = 2')
    expect(lines[3]).toBe('    └── c (uint8) = 3')
  })

  it('emits every node regardless of depth -- there is no collapse notion here', () => {
    const text = renderTreeAsText(deepChain())
    expect(text).toContain('n6 (void)')
  })

  it('uses the supplied formatValue to override the raw value', () => {
    const node: TreeNode = { id: 0, parent_id: 0, type: 'uint8', role: 'codec(video)', value: '2' }
    expect(renderTreeAsText(node, () => 'H265')).toBe('codec(video) (uint8) = H265')
  })

  it('shows size instead of value for a variable-width node', () => {
    const node: TreeNode = { id: 0, parent_id: 0, type: 'bytes', role: 'frame_data', size: 42 }
    expect(renderTreeAsText(node)).toBe('frame_data (bytes) size=42')
  })
})
