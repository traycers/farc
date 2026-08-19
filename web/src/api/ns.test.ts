import { describe, expect, it } from 'vitest'
import { nsFromDate, nsToDate, nsToDisplayString, nsToDisplayStringPrecise, parseCandidatesJSON, quoteBigintFields } from './ns'

describe('quoteBigintFields', () => {
  it('quotes only the named integer fields, leaving everything else untouched', () => {
    const text = '{"begin":1234567890123456789,"end":9876543210987654321,"index":3,"uuid":"abc"}'
    const quoted = quoteBigintFields(text, ['begin', 'end'])
    expect(JSON.parse(quoted)).toEqual({
      begin: '1234567890123456789',
      end: '9876543210987654321',
      index: 3,
      uuid: 'abc',
    })
  })

  it('quotes every occurrence across multiple objects, e.g. a nested array', () => {
    const text = '[{"channel":1,"begin":100,"end":200},{"channel":2,"begin":300,"end":400}]'
    const quoted = quoteBigintFields(text, ['begin', 'end'])
    const parsed = JSON.parse(quoted) as { channel: number; begin: string; end: string }[]
    expect(parsed).toEqual([
      { channel: 1, begin: '100', end: '200' },
      { channel: 2, begin: '300', end: '400' },
    ])
  })
})

describe('parseCandidatesJSON', () => {
  it('parses begin/end as bigint, beyond Number.MAX_SAFE_INTEGER precision', () => {
    const text = '[{"index":0,"uuid":"u1","begin":1800000000123456789,"end":1800000000987654321}]'
    const got = parseCandidatesJSON(text)
    expect(got).toEqual([
      { index: 0, uuid: 'u1', begin: 1800000000123456789n, end: 1800000000987654321n },
    ])
  })
})

describe('nsFromDate/nsToDate', () => {
  it('round-trips a Date through unix-ns', () => {
    const d = new Date('2026-08-18T12:00:00.000Z')
    expect(nsToDate(nsFromDate(d)).getTime()).toBe(d.getTime())
  })
})

describe('nsToDisplayStringPrecise', () => {
  it('appends 6-digit microseconds after the whole-second display string', () => {
    const base = nsFromDate(new Date('2026-01-01T00:00:00.000Z'))
    const ns = base + 123_456_789n
    expect(nsToDisplayStringPrecise(ns)).toBe(`${nsToDisplayString(ns)}.123456`)
  })

  it('zero-pads microseconds under 6 digits', () => {
    const base = nsFromDate(new Date('2026-01-01T00:00:00.000Z'))
    const ns = base + 7_000n // 7 microseconds
    expect(nsToDisplayStringPrecise(ns)).toBe(`${nsToDisplayString(ns)}.000007`)
  })

  it('distinguishes two frames that land in the same second', () => {
    const base = nsFromDate(new Date('2026-01-01T00:00:00.000Z'))
    const a = nsToDisplayStringPrecise(base + 1_000n)
    const b = nsToDisplayStringPrecise(base + 2_000n)
    expect(a).not.toBe(b)
  })
})
