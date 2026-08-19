import { describe, expect, it } from 'vitest'
import { formatDecodedValue } from './fblockTreeFormat'

describe('formatDecodedValue', () => {
  it('decodes video codec 1 as H264', () => {
    expect(formatDecodedValue('codec(video)', '1')).toBe('H264')
  })

  it('decodes video codec 2 as H265', () => {
    expect(formatDecodedValue('codec(video)', '2')).toBe('H265')
  })

  it('decodes video codec 0 as uninitialized', () => {
    expect(formatDecodedValue('codec(video)', '0')).toBe('uninitialized')
  })

  it('falls back to unknown(N) for an unrecognized video codec', () => {
    expect(formatDecodedValue('codec(video)', '99')).toBe('unknown(99)')
  })

  it.each([
    ['1', 'PCM'],
    ['2', 'AAC'],
    ['3', 'PCMA'],
    ['4', 'PCMU'],
  ])('decodes audio codec %s as %s', (value, expected) => {
    expect(formatDecodedValue('codec(audio)', value)).toBe(expected)
  })

  it('decodes audio codec 0 as uninitialized', () => {
    expect(formatDecodedValue('codec(audio)', '0')).toBe('uninitialized')
  })

  it('falls back to unknown(N) for an unrecognized audio codec', () => {
    expect(formatDecodedValue('codec(audio)', '99')).toBe('unknown(99)')
  })

  it.each([
    ['73', 'I'],
    ['80', 'P'],
  ])('decodes frame_kind %s as %s', (value, expected) => {
    expect(formatDecodedValue('frame_kind', value)).toBe(expected)
  })

  it('falls back to unknown(N) for an unrecognized frame_kind', () => {
    expect(formatDecodedValue('frame_kind', '99')).toBe('unknown(99)')
  })
})
