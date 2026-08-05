import { parseCandidatesJSON, type Candidate } from './ns'

const BASE = '/api/farcd'

// Geometry has no json tags in Go (internal/storage/geometry.go) so its
// field names serialize verbatim -- PascalCase, unlike everything else here.
export type Geometry = {
  FblockSize: number
  N: number
  MaxChannels: number
}

export type Retention = { days: number }

export type Params = {
  fchunk_size: number
  read_chunk_size?: number
  write_mode: string
  retention: Retention
  min_container_share: number
}

export type StorageInfo = {
  id: string
  path: string
  geometry: Geometry
}

async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

export async function listStorages(): Promise<StorageInfo[]> {
  return (await ok(await fetch(`${BASE}/storages`))).json()
}

export type CreateStorageInput = {
  id: string
  path: string
  geometry: Geometry
  params: Params
  force: boolean
  catalog_path: string
  backend: string
}

export async function createStorage(input: CreateStorageInput): Promise<StorageInfo> {
  return (
    await ok(
      await fetch(`${BASE}/storages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    )
  ).json()
}

export async function patchStorage(
  id: string,
  patch: { retention_days?: number; write_mode?: string },
): Promise<void> {
  await ok(
    await fetch(`${BASE}/storages/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(patch),
    }),
  )
}

export async function candidates(
  storage: string,
  channel: number,
  t1: bigint,
  t2: bigint,
  confirm?: boolean,
): Promise<Candidate[]> {
  const url = `${BASE}/storages/${encodeURIComponent(storage)}/candidates?channel=${channel}&t1=${t1}&t2=${t2}${confirm ? '&confirm=true' : ''}`
  const text = await (await ok(await fetch(url))).text()
  return parseCandidatesJSON(text)
}

export async function setProtected(storage: string, uuid: string, value: boolean): Promise<void> {
  await ok(
    await fetch(`${BASE}/storages/${encodeURIComponent(storage)}/fcontainers/${uuid}/protected`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ value }),
    }),
  )
}

// prerecord/postrecord are short buffer durations (seconds), not absolute
// epoch timestamps -- plain numbers are safe here, unlike Candidate's
// begin/end (see api/ns.ts).
export async function setCapturePolicy(
  channel: number,
  type: 'continuous' | 'event',
  prerecordNs: number,
  postrecordNs: number,
): Promise<void> {
  await ok(
    await fetch(`${BASE}/channels/${channel}/capture-policy`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type,
        params: { prerecord_ns: prerecordNs, postrecord_ns: postrecordNs },
      }),
    }),
  )
}

// No timestamp param: farcd defaults `t` to its own wall-clock time when the
// body is empty (internal/api/channels.go), which sidesteps having to encode
// an absolute epoch-ns bigint into a JSON body at all (JSON.stringify throws
// on a raw bigint, and Go's uint64 field rejects a quoted-string number).
export async function triggerEvent(channel: number): Promise<void> {
  await ok(await fetch(`${BASE}/channels/${channel}/events`, { method: 'POST' }))
}

// No from_time param, same reasoning as triggerEvent above: farcd defaults
// it to "now" (no queue replay) when the body is empty.
export async function startRecording(channel: number): Promise<void> {
  await ok(await fetch(`${BASE}/channels/${channel}/recording/start`, { method: 'POST' }))
}

export async function stopRecording(channel: number): Promise<void> {
  await ok(await fetch(`${BASE}/channels/${channel}/recording/stop`, { method: 'POST' }))
}

export type CapturePolicyInput = {
  type: 'continuous' | 'event'
  // Durations (seconds-scale), not absolute epoch timestamps -- plain
  // numbers are safe here, same reasoning as setCapturePolicy above.
  max_deferred_start_ns?: number
  prerecord_ns?: number
  postrecord_ns?: number
}

export type ChannelInfo = {
  channel: number
  rtsp_url: string
  storage: string
  capture_policy_type: 'continuous' | 'event'
  prerecord_ns: number
  postrecord_ns: number
}

export async function listChannels(): Promise<ChannelInfo[]> {
  return (await ok(await fetch(`${BASE}/channels`))).json()
}

export async function createChannel(input: {
  id: number
  rtsp_url: string
  storage: string
  capture_policy: CapturePolicyInput
}): Promise<ChannelInfo> {
  return (
    await ok(
      await fetch(`${BASE}/channels`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    )
  ).json()
}

export async function updateChannel(
  id: number,
  input: { rtsp_url: string; storage: string; capture_policy: CapturePolicyInput },
): Promise<ChannelInfo> {
  return (
    await ok(
      await fetch(`${BASE}/channels/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(input),
      }),
    )
  ).json()
}

export async function removeChannel(id: number): Promise<void> {
  await ok(await fetch(`${BASE}/channels/${id}`, { method: 'DELETE' }))
}
