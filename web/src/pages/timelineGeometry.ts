import type { Segment } from '../api/hls'

export type Rect = { leftPct: number; widthPct: number }

// segmentToRect maps seg onto a 0-100% horizontal position within
// [rangeStart,rangeEnd), for TimelineBar's absolutely-positioned segment
// bars.
export function segmentToRect(seg: Segment, rangeStart: bigint, rangeEnd: bigint): Rect {
  const span = rangeEnd - rangeStart
  const leftPct = (Number(seg.begin - rangeStart) / Number(span)) * 100
  const widthPct = (Number(seg.end - seg.begin) / Number(span)) * 100
  return { leftPct, widthPct }
}

// fractionToNs maps a 0..1 click fraction (e.g. clientX offset / bar width)
// back to an absolute ns timestamp within [rangeStart,rangeEnd], clamping
// out-of-bounds fractions rather than extrapolating past the visible range.
export function fractionToNs(rangeStart: bigint, rangeEnd: bigint, fraction: number): bigint {
  const clamped = Math.min(1, Math.max(0, fraction))
  const span = rangeEnd - rangeStart
  return rangeStart + BigInt(Math.round(Number(span) * clamped))
}
