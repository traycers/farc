import type { CapturePolicyInput } from './farcd'

const BASE = '/api/apid'

// WriteResult mirrors internal/apid/server.go's writeResultResponse --
// each side's outcome independently ({"farcd":"ok"|"error: ...",
// "mediamtx":"ok"|"error: ..."}), .scratch/live-page/issues/01-apid-server.md.
export type WriteResult = { farcd: string; mediamtx: string }

// writeResult parses apid's write-endpoint response and throws unless both
// sides report "ok" -- apid returns HTTP 207 (still a 2xx status, so
// fetch's own res.ok is true) for a partial failure, so the body's
// farcd/mediamtx fields, not just the HTTP status, are what actually
// decide success here. A non-2xx/207 status (e.g. 400 on a malformed
// request) falls back to farcd.ts's ok()-style "status statusText: body"
// message.
async function writeResult(res: Response): Promise<WriteResult> {
  const text = await res.text()
  let body: (WriteResult & { error?: string }) | undefined
  try {
    body = JSON.parse(text)
  } catch {
    body = undefined
  }

  if (res.status === 200 || res.status === 201 || res.status === 207) {
    const result = body as WriteResult
    if (result.farcd !== 'ok' || result.mediamtx !== 'ok') {
      throw new Error(`apid: farcd=${result.farcd}, mediamtx=${result.mediamtx}`)
    }
    return result
  }

  const message = body?.error ?? text
  throw new Error(`${res.status} ${res.statusText}${message ? `: ${message}` : ''}`)
}

// ok mirrors farcd.ts's own helper: throws with the response body on a
// non-2xx status, else passes the response through.
async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

// getLiveURLs is a single batch lookup of WHEP playback URLs, one request
// regardless of how many channels are checked on the Live page -- never
// one request per channel (.scratch/live-page/issues/01-apid-server.md).
export async function getLiveURLs(ids: number[]): Promise<Record<number, string>> {
  if (ids.length === 0) return {}
  const body: { urls: Record<string, string> } = await (
    await ok(await fetch(`${BASE}/channels/live-urls?ids=${ids.join(',')}`))
  ).json()
  const out: Record<number, string> = {}
  for (const [id, url] of Object.entries(body.urls)) {
    out[Number(id)] = url
  }
  return out
}

// getCameraURL returns a channel's camera RTSP URL, read back from
// mediamtx (the only place it's stored -- see internal/apid/server.go's
// handleGetChannel). Used to prefill the edit-channel form's rtsp_url
// field: farcd's own stored rtsp_url for a channel is mediamtx's re-serve
// address, not the camera's, so it can't be used for this
// (.scratch/live-page/spec.md).
export async function getCameraURL(id: number): Promise<string> {
  const body: { camera_rtsp_url: string } = await (await ok(await fetch(`${BASE}/channels/${id}`))).json()
  return body.camera_rtsp_url
}

// rtsp_url here is the camera's real RTSP URL -- apid, not this module or
// its callers, rewrites it to mediamtx's re-serve address before it
// reaches farcd (.scratch/live-page/spec.md).
export async function createChannel(input: {
  id: number
  rtsp_url: string
  storage: string
  capture_policy: CapturePolicyInput
  name: string
}): Promise<WriteResult> {
  return writeResult(
    await fetch(`${BASE}/channels`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }),
  )
}

export async function updateChannel(
  id: number,
  input: { rtsp_url: string; storage: string; capture_policy: CapturePolicyInput; name: string },
): Promise<WriteResult> {
  return writeResult(
    await fetch(`${BASE}/channels/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }),
  )
}

export async function removeChannel(id: number): Promise<WriteResult> {
  return writeResult(await fetch(`${BASE}/channels/${id}`, { method: 'DELETE' }))
}
