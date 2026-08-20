import { describe, expect, it } from 'vitest'
import { advance, computeDataRange, hasAnySegments, isAliveAt, nextSegmentStart, prevSegmentStart } from './playerTimeline'
import type { ChannelTimeline } from '../api/hls'

const ch1: ChannelTimeline = { channel: 1, segments: [{ begin: 0n, end: 100n }, { begin: 200n, end: 300n }] }
const ch2: ChannelTimeline = { channel: 2, segments: [{ begin: 150n, end: 250n }] }

describe('isAliveAt', () => {
  it('true when any channel has a segment covering t', () => {
    expect(isAliveAt([ch1, ch2], 50n)).toBe(true) // ch1's first segment
    expect(isAliveAt([ch1, ch2], 175n)).toBe(true) // ch2's segment, ch1 gapped here
  })
  it('false when every channel is gapped at t', () => {
    expect(isAliveAt([ch1, ch2], 120n)).toBe(false)
  })
  it('boundary is inclusive of begin and end', () => {
    expect(isAliveAt([ch1], 0n)).toBe(true)
    expect(isAliveAt([ch1], 100n)).toBe(true)
    expect(isAliveAt([ch1], 101n)).toBe(false)
  })
})

describe('nextSegmentStart', () => {
  it('nearest segment begin strictly after t, across every channel', () => {
    expect(nextSegmentStart([ch1, ch2], 120n)).toBe(150n) // ch2's begin, before ch1's 200
  })
  it('null when no channel has any segment starting after t', () => {
    expect(nextSegmentStart([ch1, ch2], 300n)).toBeNull()
  })
})

describe('prevSegmentStart', () => {
  it('nearest segment begin strictly before t, across every channel', () => {
    expect(prevSegmentStart([ch1, ch2], 220n)).toBe(200n)
  })
  it('null when no channel has any segment starting before t', () => {
    expect(prevSegmentStart([ch1, ch2], 0n)).toBeNull()
  })
})

describe('computeDataRange', () => {
  it('spans the min begin and max end across every channel, floored/ceiled to whole seconds', () => {
    const a: ChannelTimeline = { channel: 1, segments: [{ begin: 1_500_000_000n, end: 2_400_000_000n }] }
    const b: ChannelTimeline = { channel: 2, segments: [{ begin: 3_100_000_000n, end: 3_900_000_000n }] }
    expect(computeDataRange([a, b], 0n, 999n)).toEqual({ start: 1_000_000_000n, end: 4_000_000_000n })
  })

  it('falls back to the given range when no segments exist at all', () => {
    const empty: ChannelTimeline = { channel: 1, segments: [] }
    expect(computeDataRange([empty], 111n, 222n)).toEqual({ start: 111n, end: 222n })
  })
})

describe('hasAnySegments', () => {
  it('true when at least one channel has at least one segment', () => {
    expect(hasAnySegments([ch1])).toBe(true)
  })
  it('false when every channel has no segments', () => {
    const empty: ChannelTimeline = { channel: 1, segments: [] }
    expect(hasAnySegments([empty])).toBe(false)
  })
  it('false for an empty channel list', () => {
    expect(hasAnySegments([])).toBe(false)
  })
})

describe('advance', () => {
  it('continue: at least one visible channel is alive at t', () => {
    expect(advance([ch1, ch2], 175n)).toEqual({ kind: 'continue' })
  })
  it('skip: every channel gapped at t, but a later segment exists somewhere', () => {
    expect(advance([ch1, ch2], 120n)).toEqual({ kind: 'skip', to: 150n })
  })
  it('end: every channel gapped at t and nothing starts later either', () => {
    // 301n is just past ch1's last segment end (300, inclusive) -- both
    // channels are gapped here and no segment begins after it either.
    expect(advance([ch1, ch2], 301n)).toEqual({ kind: 'end' })
  })
})
