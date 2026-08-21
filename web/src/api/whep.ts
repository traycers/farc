// Minimal WHEP (WebRTC-HTTP Egress Protocol) client for live playback via
// mediamtx (.scratch/live-page/spec.md) -- no npm dependency, just
// RTCPeerConnection + fetch, matching PLAN.md's stated minimal-dependency
// scope for this app. This is the one live tile's actual WebRTC wiring,
// deliberately extracted out of LiveVideoTile.tsx so LivePage.test.tsx can
// mock that component wholesale, the same way PlayerPage.test.tsx mocks
// VideoTile -- see LiveVideoTile.tsx's own doc comment.
export async function connectWhep(url: string, video: HTMLVideoElement, signal: AbortSignal): Promise<void> {
  const pc = new RTCPeerConnection()
  let resourceUrl: string | null = null

  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })
  pc.ontrack = (event) => {
    video.srcObject = event.streams[0]
  }

  signal.addEventListener('abort', () => {
    pc.close()
    if (resourceUrl) {
      fetch(resourceUrl, { method: 'DELETE' }).catch(() => {})
    }
  })

  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)

  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: offer.sdp,
  })
  if (!res.ok) {
    throw new Error(`whep: POST ${url}: ${res.status} ${res.statusText}`)
  }
  // The WHEP resource URL to DELETE on teardown -- relative to url per the
  // spec, hence resolving it against url rather than treating it as
  // already-absolute. url itself may be relative too (a same-origin nginx
  // proxy path, e.g. /api/whep/1/whep) -- new URL()'s base argument must be
  // an absolute URL, so resolve url against the page origin first.
  const location = res.headers.get('Location')
  resourceUrl = location ? new URL(location, new URL(url, window.location.href)).toString() : null

  const answerSdp = await res.text()
  await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp })
}
