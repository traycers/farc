import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import FblockTree from './FblockTree'
import type { TreeNode } from '../api/fblockTree'

// A 7-level-deep chain (depth 0..6), one child per node, so default-depth
// (root=depth 0, levels 0-4 open) behavior is unambiguous: n5 (depth 5) is
// the first node whose OWN children (n6, depth 6) are closed by default.
function deepChain(): TreeNode {
  let node: TreeNode = { id: 6, parent_id: 5, type: 'void', role: 'n6' }
  for (let depth = 5; depth >= 0; depth--) {
    node = { id: depth, parent_id: Math.max(depth - 1, 0), type: 'void', role: `n${depth}`, children: [node] }
  }
  return node
}

describe('FblockTree collapse/expand', () => {
  it('renders down to depth 5 open by default, hiding depth 6', () => {
    render(<FblockTree root={deepChain()} />)
    for (let d = 0; d <= 5; d++) {
      expect(screen.getByText(new RegExp(`^n${d} `))).toBeInTheDocument()
    }
    expect(screen.queryByText(/^n6 /)).not.toBeInTheDocument()
  })

  it('reveals a hidden child when its parent row is clicked', () => {
    render(<FblockTree root={deepChain()} />)
    expect(screen.queryByText(/^n6 /)).not.toBeInTheDocument()
    fireEvent.click(screen.getByText(/^n5 /))
    expect(screen.getByText(/^n6 /)).toBeInTheDocument()
  })

  it('collapses an open node when its row is clicked', () => {
    render(<FblockTree root={deepChain()} />)
    expect(screen.getByText(/^n1 /)).toBeInTheDocument()
    fireEvent.click(screen.getByText(/^n0 /))
    expect(screen.queryByText(/^n1 /)).not.toBeInTheDocument()
  })

  it('"expand all" reveals every node, including past depth 5', () => {
    render(<FblockTree root={deepChain()} />)
    fireEvent.click(screen.getByRole('button', { name: /expand all/i }))
    expect(screen.getByText(/^n6 /)).toBeInTheDocument()
  })

  it('"collapse all" hides every node below the root', () => {
    render(<FblockTree root={deepChain()} />)
    fireEvent.click(screen.getByRole('button', { name: /collapse all/i }))
    expect(screen.getByText(/^n0 /)).toBeInTheDocument()
    expect(screen.queryByText(/^n1 /)).not.toBeInTheDocument()
  })
})
