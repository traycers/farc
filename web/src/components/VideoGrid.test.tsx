import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import VideoGrid from './VideoGrid'

vi.mock('./VideoTile', () => ({
  default: ({ channel }: { channel: number }) => <div data-testid="mock-tile">tile-{channel}</div>,
}))

describe('VideoGrid', () => {
  it('renders a tile per channel plus empty cells, in the layout grid shape', () => {
    render(
      <VideoGrid
        channelIds={[1, 2, 3]}
        getTileProps={(ch) => ({
          channel: ch,
          segmentUrl: null,
          seekToSec: 0,
          playing: false,
          muted: true,
          active: false,
          onClick: () => {},
        })}
      />,
    )

    const grid = screen.getByTestId('player-video-grid')
    expect(grid.style.gridTemplateColumns).toBe('repeat(2, 1fr)')
    expect(grid.style.gridTemplateRows).toBe('repeat(2, 1fr)')

    const tiles = screen.getAllByTestId('mock-tile')
    expect(tiles.map((t) => t.textContent)).toEqual(['tile-1', 'tile-2', 'tile-3'])
    // 3 channels -> 2x2 = 4 cells, 1 left empty.
    expect(screen.getAllByTestId('player-video-grid-empty-cell')).toHaveLength(1)
  })

  it('a single channel renders one tile, no empty cells', () => {
    render(
      <VideoGrid
        channelIds={[7]}
        getTileProps={(ch) => ({
          channel: ch,
          segmentUrl: null,
          seekToSec: 0,
          playing: false,
          muted: true,
          active: false,
          onClick: () => {},
        })}
      />,
    )
    expect(screen.getAllByTestId('mock-tile')).toHaveLength(1)
    expect(screen.queryAllByTestId('player-video-grid-empty-cell')).toHaveLength(0)
  })
})
