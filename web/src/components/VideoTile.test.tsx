import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import VideoTile from './VideoTile'

describe('VideoTile', () => {
  it('shows a placeholder overlay when there is no segment to play at the current instant', () => {
    render(
      <VideoTile channel={1} segmentUrl={null} seekToSec={0} playing={false} muted active={false} onClick={() => {}} />,
    )
    expect(screen.getByTestId('player-video-tile-placeholder')).toBeInTheDocument()
  })

  it('renders no placeholder once a segment is available', () => {
    render(
      <VideoTile
        channel={1}
        segmentUrl="/api/hls/channels/1/hls/0/1000/playlist.m3u8"
        seekToSec={0}
        playing={false}
        muted
        active={false}
        onClick={() => {}}
      />,
    )
    expect(screen.queryByTestId('player-video-tile-placeholder')).not.toBeInTheDocument()
  })

  it('reflects muted/active props on the underlying video element and container class', () => {
    render(
      <VideoTile channel={1} segmentUrl={null} seekToSec={0} playing={false} muted={false} active onClick={() => {}} />,
    )
    const video = document.querySelector('video') as HTMLVideoElement
    expect(video.muted).toBe(false)
    expect(screen.getByTestId('player-video-tile').className).toContain('player-video-tile-active')
  })

  it('calls onClick when the tile is clicked', () => {
    const onClick = vi.fn()
    render(<VideoTile channel={1} segmentUrl={null} seekToSec={0} playing={false} muted active={false} onClick={onClick} />)
    fireEvent.click(screen.getByTestId('player-video-tile'))
    expect(onClick).toHaveBeenCalled()
  })
})
