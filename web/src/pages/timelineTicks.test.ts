import { describe, expect, it } from 'vitest'
import { computeTicks } from './timelineTicks'
import { nsToDisplayString } from '../api/ns'

function expectedLabel(ns: bigint, withSeconds: boolean): string {
  const s = nsToDisplayString(ns) // 'YYYY-MM-DDTHH:MM:SS'
  return withSeconds ? s.slice(11, 19) : s.slice(11, 16)
}

const HOUR_NS = 3_600_000_000_000n

describe('computeTicks', () => {
  it('picks a round 10-minute step for a 1-hour range at ample width', () => {
    const ticks = computeTicks(0n, HOUR_NS, 800)
    expect(ticks).toHaveLength(7) // 0,10,20,...,60 minutes
    expect(ticks[1].ns - ticks[0].ns).toBe(600_000_000_000n)
    expect(ticks[0]).toEqual({ ns: 0n, label: expectedLabel(0n, false), leftPct: 0 })
    expect(ticks[6]).toEqual({ ns: HOUR_NS, label: expectedLabel(HOUR_NS, false), leftPct: 100 })
  })

  it('thins to a coarser 30-minute step for the same range at a narrower width', () => {
    const ticks = computeTicks(0n, HOUR_NS, 150) // maxLabels = floor(150/80) = 1 -> clamped to 2
    expect(ticks).toHaveLength(3) // 0, 30, 60 minutes
    expect(ticks[1].ns - ticks[0].ns).toBe(1_800_000_000_000n)
  })

  it('switches to HH:MM:SS labels once the step drops below a minute', () => {
    const ticks = computeTicks(0n, 10_000_000_000n, 800) // 10s range, maxLabels=10 -> 1s step
    expect(ticks).toHaveLength(11) // 0..10s inclusive
    expect(ticks[1].ns - ticks[0].ns).toBe(1_000_000_000n)
    expect(ticks[1].label).toBe(expectedLabel(ticks[1].ns, true))
  })

  it('never drops below 2 ticks even at a degenerately narrow width', () => {
    const ticks = computeTicks(0n, HOUR_NS, 1)
    expect(ticks.length).toBeGreaterThanOrEqual(2)
  })
})
