import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import PoolStatusList from './PoolStatusList'
import type { PoolSlot } from '../api/pool'

function slot(overrides: Partial<PoolSlot>): PoolSlot {
  return {
    state: 'free',
    prolog_size: 0,
    catalog_size: 0,
    content_size: 0,
    toc_size: 0,
    epilog_size: 0,
    ...overrides,
  }
}

describe('PoolStatusList', () => {
  it('renders exactly one row per slot, in order, each carrying its state as a CSS class', () => {
    const slots = [slot({ state: 'active', index: 3 }), slot({ state: 'free' }), slot({ state: 'closing', index: 3 })]
    render(<PoolStatusList slots={slots} fblockSize={1000} />)

    const rows = screen.getAllByTestId('pool-status-row')
    expect(rows).toHaveLength(3)
    expect(rows[0].querySelector('.pool-slot-square')?.className).toContain('state-active')
    expect(rows[1].querySelector('.pool-slot-square')?.className).toContain('state-free')
    expect(rows[2].querySelector('.pool-slot-square')?.className).toContain('state-closing')
  })

  it('sizes each of the 5 section boxes proportionally to fblockSize, leaving unused space as a remainder', () => {
    const slots = [
      slot({ state: 'active', prolog_size: 100, catalog_size: 50, content_size: 200, toc_size: 128, epilog_size: 20 }),
    ]
    render(<PoolStatusList slots={slots} fblockSize={1000} />)

    const row = screen.getByTestId('pool-status-row')
    // Hand-computed (bytes / fblockSize * 100), independent of the
    // component's own calculation: 100/1000, 50/1000, 200/1000, 128/1000,
    // 20/1000.
    expect((row.querySelector('.pool-section-prolog') as HTMLElement).style.width).toBe('10%')
    expect((row.querySelector('.pool-section-catalog') as HTMLElement).style.width).toBe('5%')
    expect((row.querySelector('.pool-section-content') as HTMLElement).style.width).toBe('20%')
    expect((row.querySelector('.pool-section-toc') as HTMLElement).style.width).toBe('12.8%')
    expect((row.querySelector('.pool-section-epilog') as HTMLElement).style.width).toBe('2%')
  })

  // Regression test for .scratch/fblocks-ui/issues/
  // 11-pool-bar-nested-percentage-collapses-to-zero.md: each section must be
  // positioned directly against .pool-section-bar via left/right (NOT
  // nested inside an intermediate flex wrapper -- a wrapper with no
  // explicit width has an indeterminate size, against which a percentage
  // width child resolves to 0 in every browser, invisible from a plain
  // `.style.width` assertion since that string stays correct regardless).
  it('positions prolog/catalog/content via a left offset and toc/epilog via a right offset, growing toward each other, with no intermediate group wrapper', () => {
    const slots = [
      slot({ state: 'active', prolog_size: 100, catalog_size: 50, content_size: 200, toc_size: 128, epilog_size: 20 }),
    ]
    render(<PoolStatusList slots={slots} fblockSize={1000} />)

    const row = screen.getByTestId('pool-status-row')
    expect(row.querySelector('.pool-section-left')).toBeNull()
    expect(row.querySelector('.pool-section-right')).toBeNull()

    const bar = row.querySelector('.pool-section-bar') as HTMLElement
    const barChildClasses = Array.from(bar.children).map((c) => c.className)
    expect(barChildClasses).toEqual([
      'pool-section pool-section-prolog',
      'pool-section pool-section-catalog',
      'pool-section pool-section-content',
      'pool-section pool-section-toc',
      'pool-section pool-section-epilog',
    ])

    // Left group: each section starts where the previous one ended.
    expect((row.querySelector('.pool-section-prolog') as HTMLElement).style.left).toBe('0%')
    expect((row.querySelector('.pool-section-catalog') as HTMLElement).style.left).toBe('10%')
    expect((row.querySelector('.pool-section-content') as HTMLElement).style.left).toBe('15%')

    // Right group: epilog is flush against the right edge; toc sits just
    // before it (epilog's width away from the edge).
    expect((row.querySelector('.pool-section-epilog') as HTMLElement).style.right).toBe('0%')
    expect((row.querySelector('.pool-section-toc') as HTMLElement).style.right).toBe('2%')
  })
})
