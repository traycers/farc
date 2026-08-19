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

// Quotes every occurrence of the named integer fields before handing text to
// JSON.parse, which would otherwise coerce them through a float64 and lose
// precision -- the native parser has no bigint mode. Field-name based (not
// depth-based), so it works unchanged on any nesting shape.
export function quoteBigintFields(text: string, fields: readonly string[]): string {
  const pattern = new RegExp(`"(${fields.join('|')})":(\\d+)`, 'g')
  return text.replace(pattern, '"$1":"$2"')
}

export function parseCandidatesJSON(text: string): Candidate[] {
  const quoted = quoteBigintFields(text, ['begin', 'end'])
  const raw = JSON.parse(quoted) as RawCandidate[]
  return raw.map((c) => ({ index: c.index, uuid: c.uuid, begin: BigInt(c.begin), end: BigInt(c.end) }))
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

// Microsecond-precision sibling of nsToDisplayString, for contexts where
// second granularity isn't enough to tell two events apart (e.g. two frames
// in the fblock tree, .scratch/fblocks-ui/issues/12-tree-codec-frame-kind-
// and-precise-time.md) -- Date only has millisecond resolution, so the
// microseconds come from the raw ns remainder, not from a Date field.
export function nsToDisplayStringPrecise(ns: bigint): string {
  const micros = (ns % 1_000_000_000n) / 1_000n
  return `${nsToDisplayString(ns)}.${micros.toString().padStart(6, '0')}`
}
