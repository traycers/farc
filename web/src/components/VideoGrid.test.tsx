import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import VideoGrid from './VideoGrid'

// A generic grid must render whatever Tile it's given -- not a specific
// hardcoded component -- so this fake tile (not VideoTile) is the real
// seam under test (.scratch/live-page/issues/04: VideoGrid is shared by
// PlayerPage's VideoTile and LivePage's LiveVideoTile).
function FakeTile({ channel }: { channel: number }) {
  return <div data-testid="fake-tile">tile-{channel}</div>
}

describe('VideoGrid', () => {
  it('renders a Tile per channel plus empty cells, in the layout grid shape', () => {
    render(<VideoGrid channelIds={[1, 2, 3]} Tile={FakeTile} getTileProps={(ch) => ({ channel: ch })} />)

    const grid = screen.getByTestId('player-video-grid')
    expect(grid.style.gridTemplateColumns).toBe('repeat(2, 1fr)')
    expect(grid.style.gridTemplateRows).toBe('repeat(2, 1fr)')

    const tiles = screen.getAllByTestId('fake-tile')
    expect(tiles.map((t) => t.textContent)).toEqual(['tile-1', 'tile-2', 'tile-3'])
    // 3 channels -> 2x2 = 4 cells, 1 left empty.
    expect(screen.getAllByTestId('player-video-grid-empty-cell')).toHaveLength(1)
  })

  it('a single channel renders one tile, no empty cells', () => {
    render(<VideoGrid channelIds={[7]} Tile={FakeTile} getTileProps={(ch) => ({ channel: ch })} />)

    expect(screen.getAllByTestId('fake-tile')).toHaveLength(1)
    expect(screen.queryAllByTestId('player-video-grid-empty-cell')).toHaveLength(0)
  })
})
