import { layoutCells } from '../pages/playerLayout'
import VideoTile from './VideoTile'

type TileProps = React.ComponentProps<typeof VideoTile>

type VideoGridProps = {
  channelIds: number[]
  getTileProps: (channel: number) => Omit<TileProps, 'channel'> & { channel: number }
}

// VideoGrid is a pure layout wrapper: it renders layoutCells' grid shape,
// one VideoTile per checked channel, empty cells left blank
// (.scratch/player-redesign/spec.md's automatic 1/1x2/NxN layout).
export default function VideoGrid({ channelIds, getTileProps }: VideoGridProps) {
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
          <VideoTile key={channel} {...getTileProps(channel)} />
        ),
      )}
    </div>
  )
}
