import { describe, expect, it } from 'vitest'
import { groupFrameNodes } from './frameGrouping'
import type { TreeNode } from './fblockTree'

function frame(id: number, parentId: number, role: string): TreeNode {
  return { id, parent_id: parentId, type: 'void', role }
}

function framesContainer(id: number, parentId: number, role: string, count: number, frameRole: string): TreeNode {
  const children: TreeNode[] = []
  for (let i = 0; i < count; i++) {
    children.push(frame(id + 1 + i, id, frameRole))
  }
  return { id, parent_id: parentId, type: 'void', role, children }
}

describe('groupFrameNodes', () => {
  it('groups a run of exactly 100 video frames into one group node', () => {
    const root = framesContainer(0, 0, 'frames(video)', 100, 'frame(video)')
    const grouped = groupFrameNodes(root)
    expect(grouped.children).toHaveLength(1)
    const group = grouped.children![0]
    expect(group.type).toBe('group')
    expect(group.role).toBe('frame(video)')
    expect(group.children).toHaveLength(100)
  })

  it('does not group a run of 99 video frames', () => {
    const root = framesContainer(0, 0, 'frames(video)', 99, 'frame(video)')
    const grouped = groupFrameNodes(root)
    expect(grouped.children).toHaveLength(99)
    expect(grouped.children!.every((c) => c.type !== 'group')).toBe(true)
  })

  it('groups a run of exactly 500 audio frames into one group node', () => {
    const root = framesContainer(0, 0, 'frames(audio)', 500, 'frame(audio)')
    const grouped = groupFrameNodes(root)
    expect(grouped.children).toHaveLength(1)
    expect(grouped.children![0].type).toBe('group')
    expect(grouped.children![0].children).toHaveLength(500)
  })

  it('does not group a run of 499 audio frames', () => {
    const root = framesContainer(0, 0, 'frames(audio)', 499, 'frame(audio)')
    const grouped = groupFrameNodes(root)
    expect(grouped.children).toHaveLength(499)
  })

  it('leaves non-frame siblings untouched and preserves order', () => {
    const framesNode = framesContainer(10, 1, 'frames(video)', 100, 'frame(video)')
    const root: TreeNode = {
      id: 0,
      parent_id: 0,
      type: 'void',
      role: 'root',
      children: [
        { id: 1, parent_id: 0, type: 'void', role: 'channels', children: [framesNode] },
        { id: 999, parent_id: 0, type: 'uint32', role: 'stream', value: '0' },
      ],
    }
    const grouped = groupFrameNodes(root)
    expect(grouped.children).toHaveLength(2)
    expect(grouped.children![1].role).toBe('stream')
    const channels = grouped.children![0]
    expect(channels.children).toHaveLength(1)
    expect(channels.children![0].children).toHaveLength(1)
    expect(channels.children![0].children![0].type).toBe('group')
  })

  it('groups frames independently under two separate containers', () => {
    const root: TreeNode = {
      id: 0,
      parent_id: 0,
      type: 'void',
      role: 'channels',
      children: [framesContainer(1, 0, 'frames(video)', 100, 'frame(video)'), framesContainer(200, 0, 'frames(audio)', 500, 'frame(audio)')],
    }
    const grouped = groupFrameNodes(root)
    expect(grouped.children![0].children).toHaveLength(1)
    expect(grouped.children![0].children![0].type).toBe('group')
    expect(grouped.children![1].children).toHaveLength(1)
    expect(grouped.children![1].children![0].type).toBe('group')
  })

  it('assigns synthetic group ids that never collide with real ids', () => {
    const root = framesContainer(0, 0, 'frames(video)', 100, 'frame(video)')
    const grouped = groupFrameNodes(root)
    expect(grouped.children![0].id).toBeLessThan(0)
  })
})
