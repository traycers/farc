import { useEffect, useRef, useState } from 'react'
import { connectWhep } from '../api/whep'

type LiveVideoTileProps = {
  channel: number
  // null: apid has no WHEP URL for this channel yet (still loading, or the
  // channel has no mediamtx path) -- render a placeholder, don't attempt a
  // connection.
  whepUrl: string | null
  muted: boolean
  active: boolean
  onClick: () => void
}

// LiveVideoTile is one channel's tile in the Live page's grid -- a real
// WebRTC/WHEP connection to mediamtx (.scratch/live-page/spec.md), unlike
// VideoTile's bounded hls.js archive playback. jsdom has no
// RTCPeerConnection at all (unlike hls.js, which degrades gracefully), so
// this checks for it explicitly and shows an error instead of throwing --
// the same defensive-unsupported-browser shape VideoTile's own
// Hls.isSupported()/native-HLS-fallback chain already uses, which
// incidentally also keeps this component's own unit tests (run under
// jsdom) safe. Real exercising happens against a real mediamtx, not here --
// coverage in this file's own test is intentionally thin, same
// "PlayerPage/VideoTile" precedent.
export default function LiveVideoTile({ channel, whepUrl, muted, active, onClick }: LiveVideoTileProps) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setError(null)
    const video = videoRef.current
    if (!video || !whepUrl) return
    if (typeof RTCPeerConnection === 'undefined') {
      setError('Этот браузер не поддерживает WebRTC.')
      return
    }
    const controller = new AbortController()
    connectWhep(whepUrl, video, controller.signal).catch((e) => setError(String(e)))
    return () => controller.abort()
  }, [whepUrl])

  return (
    <div
      className={`live-video-tile${active ? ' live-video-tile-active' : ''}`}
      data-testid="live-video-tile"
      onClick={onClick}
    >
      <video ref={videoRef} muted={muted} playsInline autoPlay />
      {!whepUrl && !error && (
        <div className="live-video-tile-placeholder" data-testid="live-video-tile-placeholder">
          channel {channel}: нет сигнала
        </div>
      )}
      {error && (
        <div className="live-video-tile-placeholder alert-danger" data-testid="live-video-tile-placeholder">
          {error}
        </div>
      )}
    </div>
  )
}
