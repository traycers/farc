import type { ComponentType } from 'react'
import { layoutCells } from '../pages/playerLayout'

type VideoGridProps<TileProps extends { channel: number }> = {
  channelIds: number[]
  getTileProps: (channel: number) => TileProps
  // The tile component to render per cell -- VideoTile (PlayerPage's
  // bounded archive playback) or LiveVideoTile (the Live page's WebRTC
  // playback, .scratch/live-page/issues/04) are both just callers of this
  // same generic grid, not special-cased here.
  Tile: ComponentType<TileProps>
}

// VideoGrid is a pure layout wrapper: it renders layoutCells' grid shape,
// one Tile per checked channel, empty cells left blank
// (.scratch/player-redesign/spec.md's automatic 1/1x2/NxN layout).
export default function VideoGrid<TileProps extends { channel: number }>({
  channelIds,
  getTileProps,
  Tile,
}: VideoGridProps<TileProps>) {
  const { shape, cells } = layoutCells(channelIds)
  return (
    <div
      className="player-video-grid"
      data-testid="player-video-grid"
      style={{ gridTemplateColumns: `repeat(${shape.cols}, 1fr)`, gridTemplateRows: `repeat(${shape.rows}, 1fr)` }}
    >
      {cells.map((channel, i) =>
        channel === null ? (
          <div key={`empty-${i}`} className="player-video-grid-empty-cell" data-testid="player-video-grid-empty-cell" />
        ) : (
          <Tile key={channel} {...getTileProps(channel)} />
        ),
      )}
    </div>
  )
}
