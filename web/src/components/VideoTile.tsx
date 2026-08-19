import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'

type VideoTileProps = {
  channel: number
  // null: this channel has no video at the current playhead instant --
  // render a placeholder, don't touch playback (.scratch/player-redesign/
  // spec.md's gap behavior).
  segmentUrl: string | null
  seekToSec: number
  playing: boolean
  muted: boolean
  active: boolean
  onClick: () => void
}

// VideoTile is one channel's tile in the grid -- a controlled follower of
// PlayerPage's single shared playhead, not its own independent player. Real
// hls.js decode isn't meaningfully testable in jsdom (today's PlayerPage has
// no test either); coverage here is intentionally thin, the two Playwright
// specs are where this is actually exercised.
export default function VideoTile({ channel, segmentUrl, seekToSec, playing, muted, active, onClick }: VideoTileProps) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const hlsRef = useRef<Hls | null>(null)
  const [error, setError] = useState<string | null>(null)

  // (Re)load the source whenever the segment changes -- switching segments
  // means switching HLS sources, not just seeking within one.
  useEffect(() => {
    const video = videoRef.current
    hlsRef.current?.destroy()
    hlsRef.current = null
    if (!video || !segmentUrl) return
    setError(null)
    if (Hls.isSupported()) {
      const hls = new Hls()
      hlsRef.current = hls
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError(`Playback failed (${data.type}/${data.details})`)
          hls.destroy()
        }
      })
      hls.loadSource(segmentUrl)
      hls.attachMedia(video)
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = segmentUrl
      video.onerror = () => setError(`Playback failed: ${video.error?.message ?? 'unknown error'}`)
    } else {
      setError('This browser supports neither MSE (hls.js) nor native HLS playback.')
    }
    video.currentTime = seekToSec
    return () => {
      hlsRef.current?.destroy()
      hlsRef.current = null
    }
    // seekToSec deliberately excluded: only re-run this effect (and reload
    // the source) when the segment itself changes, not on every playhead
    // tick -- see the next effect for in-segment seeks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [segmentUrl])

  // A manual jump (timeline click, prev/next, gap-skip) within the segment
  // that's already loaded -- just seek, no source reload.
  useEffect(() => {
    const video = videoRef.current
    if (!video || !segmentUrl) return
    if (Math.abs(video.currentTime - seekToSec) > 0.3) video.currentTime = seekToSec
  }, [seekToSec, segmentUrl])

  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    if (playing) void video.play().catch(() => {})
    else video.pause()
  }, [playing, segmentUrl])

  return (
    <div
      className={`player-video-tile${active ? ' player-video-tile-active' : ''}`}
      data-testid="player-video-tile"
      onClick={onClick}
    >
      <video ref={videoRef} muted={muted} playsInline />
      {!segmentUrl && (
        <div className="player-video-tile-placeholder" data-testid="player-video-tile-placeholder">
          channel {channel}: нет записи
        </div>
      )}
      {error && <div className="player-video-tile-placeholder alert-danger">{error}</div>}
    </div>
  )
}
