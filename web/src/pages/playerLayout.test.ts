import { describe, expect, it } from 'vitest'
import { gridShape, layoutCells } from './playerLayout'

describe('gridShape', () => {
  it('1 channel -> 1x1', () => {
    expect(gridShape(1)).toEqual({ rows: 1, cols: 1 })
  })
  it('0 channels -> 1x1 (nothing to show, but never a 0-sized grid)', () => {
    expect(gridShape(0)).toEqual({ rows: 1, cols: 1 })
  })
  it('2 channels -> 1x2 (explicit exception, not the sqrt rule)', () => {
    expect(gridShape(2)).toEqual({ rows: 1, cols: 2 })
  })
  it('3 channels -> 2x2 (ceil(sqrt(3)))', () => {
    expect(gridShape(3)).toEqual({ rows: 2, cols: 2 })
  })
  it('4 channels -> 2x2', () => {
    expect(gridShape(4)).toEqual({ rows: 2, cols: 2 })
  })
  it('5 channels -> 3x3 (ceil(sqrt(5)))', () => {
    expect(gridShape(5)).toEqual({ rows: 3, cols: 3 })
  })
  it('9 channels -> 3x3', () => {
    expect(gridShape(9)).toEqual({ rows: 3, cols: 3 })
  })
  it('10 channels -> 4x4', () => {
    expect(gridShape(10)).toEqual({ rows: 4, cols: 4 })
  })
})

describe('layoutCells', () => {
  it('places channels row-major, leaving trailing cells null', () => {
    const { shape, cells } = layoutCells([11, 22, 33])
    expect(shape).toEqual({ rows: 2, cols: 2 })
    expect(cells).toEqual([11, 22, 33, null])
  })

  it('empty input -> 1x1 grid with a single empty cell', () => {
    expect(layoutCells([])).toEqual({ shape: { rows: 1, cols: 1 }, cells: [null] })
  })
})
