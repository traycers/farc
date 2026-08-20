import type { ChannelTimeline } from '../api/hls'

// isAliveAt is "does at least one visible channel have video at t" --
// .scratch/player-redesign/spec.md's gap-skip rule: play continues as long
// as this is true, regardless of any individual channel's own gaps.
export function isAliveAt(timelines: ChannelTimeline[], t: bigint): boolean {
  return timelines.some((ct) => ct.segments.some((s) => s.begin <= t && t <= s.end))
}

// nextSegmentStart is the smallest segment begin strictly after t, across
// every channel -- null if none (end of loaded data).
export function nextSegmentStart(timelines: ChannelTimeline[], t: bigint): bigint | null {
  let best: bigint | null = null
  for (const ct of timelines) {
    for (const s of ct.segments) {
      if (s.begin > t && (best === null || s.begin < best)) best = s.begin
    }
  }
  return best
}

// prevSegmentStart is the largest segment begin strictly before t, across
// every channel -- null if none.
export function prevSegmentStart(timelines: ChannelTimeline[], t: bigint): bigint | null {
  let best: bigint | null = null
  for (const ct of timelines) {
    for (const s of ct.segments) {
      if (s.begin < t && (best === null || s.begin > best)) best = s.begin
    }
  }
  return best
}

const NS_PER_SECOND = 1_000_000_000n

function floorToSecond(ns: bigint): bigint {
  return ns - (ns % NS_PER_SECOND)
}

function ceilToSecond(ns: bigint): bigint {
  const rem = ns % NS_PER_SECOND
  return rem === 0n ? ns : ns + (NS_PER_SECOND - rem)
}

// computeDataRange is the timeline's displayed [start,end] -- the actual
// recorded extent (min begin/max end across every visible channel's
// segments), floored/ceiled to whole seconds, per .scratch/player-redesign/
// issues/03-timeline-axis-and-data-clipped-range.md ("обрезать до целых
// секунд от записей"), not the raw search-form window. Falls back to the
// given range when no segments were returned at all.
export function computeDataRange(timelines: ChannelTimeline[], fallbackStart: bigint, fallbackEnd: bigint): { start: bigint; end: bigint } {
  let minBegin: bigint | null = null
  let maxEnd: bigint | null = null
  for (const ct of timelines) {
    for (const s of ct.segments) {
      if (minBegin === null || s.begin < minBegin) minBegin = s.begin
      if (maxEnd === null || s.end > maxEnd) maxEnd = s.end
    }
  }
  if (minBegin === null || maxEnd === null) return { start: fallbackStart, end: fallbackEnd }
  return { start: floorToSecond(minBegin), end: ceilToSecond(maxEnd) }
}

// hasAnySegments is whether a search actually matched any recorded data --
// PlayerPage's signal to show a "no records" message instead of silently
// falling back to the raw search window (computeDataRange's own fallback).
export function hasAnySegments(timelines: ChannelTimeline[]): boolean {
  return timelines.some((ct) => ct.segments.length > 0)
}

export type PlayheadStep = { kind: 'continue' } | { kind: 'skip'; to: bigint } | { kind: 'end' }

// advance is the single function the playback tick calls each interval:
// keep playing while alive, jump to the nearest next segment when every
// visible channel is simultaneously gapped, or stop once nothing is left.
export function advance(timelines: ChannelTimeline[], t: bigint): PlayheadStep {
  if (isAliveAt(timelines, t)) return { kind: 'continue' }
  const next = nextSegmentStart(timelines, t)
  if (next === null) return { kind: 'end' }
  return { kind: 'skip', to: next }
}
