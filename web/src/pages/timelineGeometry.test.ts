import { describe, expect, it } from 'vitest'
import { fractionToNs, segmentToRect } from './timelineGeometry'

describe('segmentToRect', () => {
  it('maps a segment to a left/width percentage of the visible range', () => {
    const rect = segmentToRect({ begin: 250n, end: 500n }, 0n, 1000n)
    expect(rect).toEqual({ leftPct: 25, widthPct: 25 })
  })

  it('a segment spanning the whole range is 0%..100%', () => {
    expect(segmentToRect({ begin: 0n, end: 1000n }, 0n, 1000n)).toEqual({ leftPct: 0, widthPct: 100 })
  })
})

describe('fractionToNs', () => {
  it('maps a 0..1 click fraction back to an absolute ns timestamp', () => {
    expect(fractionToNs(1_000_000n, 2_000_000n, 0.5)).toBe(1_500_000n)
  })
  it('clamps fractions outside [0,1]', () => {
    expect(fractionToNs(0n, 1000n, -0.5)).toBe(0n)
    expect(fractionToNs(0n, 1000n, 1.5)).toBe(1000n)
  })
})
