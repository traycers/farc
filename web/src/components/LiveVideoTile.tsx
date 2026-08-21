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
  // unsupported: this browser has no RTCPeerConnection at all -- a real,
  // user-facing distinction (per-browser support), shown as its own
  // message. Any other failure (mediamtx has no source yet, a transient
  // network error, ...) collapses to the same "no signal" placeholder as
  // the no-URL case -- a WHEP failure isn't actionable by the viewer and
  // the raw error (e.g. "400 Bad Request") isn't meaningful to them
  // either; the real error is still logged to the console for debugging
  // (.scratch/live-page-fixes-adjacent bug found 2026-08-21).
  const [unsupported, setUnsupported] = useState(false)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    setUnsupported(false)
    setFailed(false)
    const video = videoRef.current
    if (!video || !whepUrl) return
    if (typeof RTCPeerConnection === 'undefined') {
      setUnsupported(true)
      return
    }
    const controller = new AbortController()
    connectWhep(whepUrl, video, controller.signal).catch((e) => {
      console.error(`live tile ${channel}: WHEP connection failed`, e)
      setFailed(true)
    })
    return () => controller.abort()
  }, [whepUrl, channel])

  return (
    <div
      className={`live-video-tile${active ? ' live-video-tile-active' : ''}`}
      data-testid="live-video-tile"
      onClick={onClick}
    >
      <video ref={videoRef} muted={muted} playsInline autoPlay />
      {(!whepUrl || failed) && !unsupported && (
        <div className="live-video-tile-placeholder" data-testid="live-video-tile-placeholder">
          channel {channel}: нет сигнала
        </div>
      )}
      {unsupported && (
        <div className="live-video-tile-placeholder alert-danger" data-testid="live-video-tile-placeholder">
          Этот браузер не поддерживает WebRTC.
        </div>
      )}
    </div>
  )
}
