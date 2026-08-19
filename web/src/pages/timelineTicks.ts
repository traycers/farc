import { nsToDate } from '../api/ns'

export type Tick = { ns: bigint; label: string; leftPct: number }

// Minimum horizontal spacing (px) between adjacent tick labels, below which
// we thin down to a coarser step rather than let labels overlap -- see
// .scratch/player-redesign/issues/03-timeline-axis-and-data-clipped-range.md.
const MIN_PX_PER_TICK = 80

// "Nice" round step sizes, seconds -> 1s..24h. pickStepSeconds finds the
// smallest one at least as coarse as the ideal spacing; beyond 24h we fall
// back to whole-day multiples (multi-day ranges aren't specially handled).
const NICE_STEPS_SECONDS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]

function pickStepSeconds(idealSeconds: number): number {
  for (const s of NICE_STEPS_SECONDS) {
    if (s >= idealSeconds) return s
  }
  return Math.ceil(idealSeconds / 86400) * 86400
}

function ceilDiv(a: bigint, b: bigint): bigint {
  return (a + b - 1n) / b
}

function formatTick(ns: bigint, stepSeconds: number): string {
  const d = nsToDate(ns)
  const pad = (n: number) => String(n).padStart(2, '0')
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return stepSeconds >= 60 ? hm : `${hm}:${pad(d.getSeconds())}`
}

// computeTicks picks a round time step (via pickStepSeconds) so adjacent
// labels stay at least MIN_PX_PER_TICK apart given widthPx, then places
// ticks on that step's absolute-time grid, clipped to [rangeStart,rangeEnd].
export function computeTicks(rangeStart: bigint, rangeEnd: bigint, widthPx: number): Tick[] {
  const span = rangeEnd - rangeStart
  if (span <= 0n) return []

  const maxLabels = Math.max(2, Math.floor(widthPx / MIN_PX_PER_TICK))
  const idealStepSeconds = Number(span) / 1e9 / maxLabels
  const stepSeconds = pickStepSeconds(idealStepSeconds)
  const stepNs = BigInt(stepSeconds) * 1_000_000_000n

  const ticks: Tick[] = []
  for (let t = ceilDiv(rangeStart, stepNs) * stepNs; t <= rangeEnd; t += stepNs) {
    const leftPct = (Number(t - rangeStart) / Number(span)) * 100
    ticks.push({ ns: t, label: formatTick(t, stepSeconds), leftPct })
  }
  return ticks
}
