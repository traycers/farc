import { quoteBigintFields } from './ns'

const BASE = '/api/hls'

export type Segment = { begin: bigint; end: bigint }
export type ChannelTimeline = { channel: number; segments: Segment[] }

type RawSegment = { begin: string; end: string }
type RawChannelTimeline = { channel: number; segments: RawSegment[] }

async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

// getTimeline fetches hls_server's precomputed video-presence timeline
// (.scratch/player-redesign/issues/01-hls-server-timeline-endpoint.md) for
// every channel in one request.
export async function getTimeline(channels: number[], t1: bigint, t2: bigint): Promise<ChannelTimeline[]> {
  const url = `${BASE}/timeline?channels=${channels.join(',')}&t1=${t1}&t2=${t2}`
  const text = await (await ok(await fetch(url))).text()
  const raw = JSON.parse(quoteBigintFields(text, ['begin', 'end'])) as RawChannelTimeline[]
  return raw.map((c) => ({
    channel: c.channel,
    segments: c.segments.map((s) => ({ begin: BigInt(s.begin), end: BigInt(s.end) })),
  }))
}

// playlistUrl is a pure URL builder, no fetch -- centralizing the string
// PlayerPage.tsx used to build by hand keeps VideoTile presentational.
export function playlistUrl(channel: number, t1: bigint, t2: bigint): string {
  return `${BASE}/channels/${channel}/hls/${t1}/${t2}/playlist.m3u8`
}
