import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import FblockTreePage, { formatValue } from './FblockTreePage'
import type { TocRow, TreeNode } from '../api/fblockTree'
import { downloadTextFile } from '../lib/download'

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

vi.mock('../lib/download', () => ({ downloadTextFile: vi.fn() }))

function tocRowsWithManyVideoFrames(n: number): TocRow[] {
  const root: TocRow = { id: 0, parent_id: 0, sibling_id: 0, type: 'void', role: 'root', value_or_offset: '0', size: 0 }
  const frames: TocRow[] = Array.from({ length: n }, (_, i) => ({
    id: i + 1,
    parent_id: 0,
    sibling_id: i,
    type: 'uint8',
    role: 'frame(video)',
    value_or_offset: String(i),
    size: 0,
  }))
  return [root, ...frames]
}

vi.mock('../api/fblockTree', async () => {
  const actual = await vi.importActual<typeof import('../api/fblockTree')>('../api/fblockTree')
  return {
    ...actual,
    getFblockInfo: vi.fn(async () => ({ index: 0, state: 'ready', uuid: 'abc' })),
    getFblockTocRows: vi.fn(async () => tocRowsWithManyVideoFrames(150)),
  }
})

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/storages/s1/fblocks/0/tree']}>
      <Routes>
        <Route path="/storages/:id/fblocks/:index/tree" element={<FblockTreePage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('FblockTreePage download tree button', () => {
  it('downloads the raw (un-grouped) tree, never the display-grouped one', async () => {
    renderPage()
    const button = await screen.findByRole('button', { name: /download tree/i })
    fireEvent.click(button)

    expect(downloadTextFile).toHaveBeenCalledTimes(1)
    const [filename, content] = vi.mocked(downloadTextFile).mock.calls[0]
    expect(filename).toBe('fblock-abc-tree.txt')
    // frameGrouping.ts would collapse this 150-node run into one "150
    // frames" node -- its absence proves the raw tree was serialized.
    expect(content).not.toContain('150 frames')
    expect(content).toContain('frame(video)')
  })
})

describe('FblockTreePage TOC table link', () => {
  it('links to the toc-table page for this fblock', async () => {
    renderPage()
    const link = await screen.findByRole('link', { name: /toc table/i })
    expect(link.getAttribute('href')).toBe('/storages/s1/fblocks/0/tree/toc')
  })
})
