// Unix-nanosecond timestamps (farcd's `begin`/`end`/`time`/`t1`/`t2`) exceed
// Number.MAX_SAFE_INTEGER (9e15) at current wall-clock time (~1.8e18) -- a
// plain JSON.parse or Number() on them silently rounds. Everything on this
// axis is a bigint end to end; only URL/path construction (string
// concatenation) and this module's own parser ever touch the raw text.

export type Candidate = {
  index: number
  uuid: string
  begin: bigint
  end: bigint
}

type RawCandidate = { index: number; uuid: string; begin: string; end: string }

// Quotes the (known-position) begin/end integer literals before handing the
// text to JSON.parse, which would otherwise coerce them through a float64
// and lose precision -- the native parser has no bigint mode.
export function parseCandidatesJSON(text: string): Candidate[] {
  const quoted = text.replace(/"(begin|end)":(\d+)/g, '"$1":"$2"')
  const raw = JSON.parse(quoted) as RawCandidate[]
  return raw.map((c) => ({ index: c.index, uuid: c.uuid, begin: BigInt(c.begin), end: BigInt(c.end) }))
}

// Mirrors internal/api/fblocks.go's fblockInfo -- uuid/begin/end/channels
// are only present once state is "ready".
export type FblockInfo = {
  index: number
  state: string
  uuid?: string
  begin?: bigint
  end?: bigint
  protected: boolean
  channels?: number[]
}

type RawFblockInfo = Omit<FblockInfo, 'begin' | 'end'> & { begin?: string; end?: string }

export type FblockListPage = {
  total: number
  fblocks: FblockInfo[]
}

// Same bigint-precision problem as parseCandidatesJSON, same fix. Mirrors
// internal/api/fblocks.go's fblockListResponse envelope: {total, fblocks}.
export function parseFblocksJSON(text: string): FblockListPage {
  const quoted = text.replace(/"(begin|end)":(\d+)/g, '"$1":"$2"')
  const raw = JSON.parse(quoted) as { total: number; fblocks: RawFblockInfo[] }
  return {
    total: raw.total,
    fblocks: raw.fblocks.map((r) => ({
      ...r,
      begin: r.begin !== undefined ? BigInt(r.begin) : undefined,
      end: r.end !== undefined ? BigInt(r.end) : undefined,
    })),
  }
}

export function nsFromDate(d: Date): bigint {
  return BigInt(d.getTime()) * 1_000_000n
}

export function nsToDate(ns: bigint): Date {
  return new Date(Number(ns / 1_000_000n))
}

// Value for <input type="datetime-local">.
export function nsToLocalInputValue(ns: bigint): string {
  const d = nsToDate(ns)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function nsFromLocalInputValue(v: string): bigint {
  return nsFromDate(new Date(v))
}

// For read-only display where seconds matter (e.g. the candidates table) --
// nsToLocalInputValue intentionally truncates to minutes to match
// <input type="datetime-local">'s default step, which loses too much
// precision for two candidates that differ by only a few seconds.
export function nsToDisplayString(ns: bigint): string {
  const d = nsToDate(ns)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
