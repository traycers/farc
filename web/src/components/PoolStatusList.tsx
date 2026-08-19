import type { PoolSlot } from '../api/pool'

type PoolStatusListProps = {
  slots: PoolSlot[]
  // The Storage's fixed geometry.FblockSize -- the pool-fullness bar's
  // scale (design decision: proportional to the fblock's total capacity,
  // with unused space a visible remainder, not just relative shares among
  // the 5 known section sizes).
  fblockSize: number
}

// LEFT_SECTIONS/RIGHT_SECTIONS mirror docs/docs/archive/03-storage-format.md's
// on-disk section ordering, split into two groups that grow toward each
// other (design decision: content grows left-to-right after the fixed
// prolog/catalog, toc grows right-to-left before the fixed epilog, meeting
// in a shrinking free-space gap). Each section is positioned via
// position:absolute directly against .pool-section-bar (see index.css) --
// NOT nested inside an intermediate flex wrapper: a wrapper with no
// explicit width has an auto/indeterminate size, and CSS resolves a
// percentage width against an indeterminate containing block as 0,
// regardless of browser (.scratch/fblocks-ui/issues/
// 11-pool-bar-nested-percentage-collapses-to-zero.md) -- confirmed via
// getBoundingClientRect() in both Chromium and Firefox, not just the
// style attribute's string value, which stayed correct throughout and so
// never caught this.
const LEFT_SECTIONS: { key: keyof PoolSlot; cls: string }[] = [
  { key: 'prolog_size', cls: 'pool-section-prolog' },
  { key: 'catalog_size', cls: 'pool-section-catalog' },
  { key: 'content_size', cls: 'pool-section-content' },
]
const RIGHT_SECTIONS: { key: keyof PoolSlot; cls: string }[] = [
  { key: 'toc_size', cls: 'pool-section-toc' },
  { key: 'epilog_size', cls: 'pool-section-epilog' },
]

function pct(bytes: number, fblockSize: number): number {
  if (fblockSize <= 0) return 0
  return (bytes / fblockSize) * 100
}

// prefixOffsets returns, for each width, the sum of every earlier width in
// the array -- how far from the start (left edge) that section begins,
// given sections laid out contiguously in array order.
function prefixOffsets(widths: number[]): number[] {
  let cursor = 0
  return widths.map((w) => {
    const offset = cursor
    cursor += w
    return offset
  })
}

// suffixOffsets returns, for each width, the sum of every later width in
// the array -- how far from the end (right edge) that section begins, given
// sections laid out contiguously in array order growing toward the start.
function suffixOffsets(widths: number[]): number[] {
  const offsets = new Array<number>(widths.length)
  let cursor = 0
  for (let i = widths.length - 1; i >= 0; i--) {
    offsets[i] = cursor
    cursor += widths[i]
  }
  return offsets
}

// PoolStatusList renders one row per pool slot (always PoolTuning.Size
// rows, free slots included) above FblocksGridPage's existing squares grid
// -- .scratch/fblocks-ui/issues/04-pool-status-list-plan.md.
export default function PoolStatusList({ slots, fblockSize }: PoolStatusListProps) {
  return (
    <div className="pool-status-list">
      {slots.map((slot, i) => {
        const leftWidths = LEFT_SECTIONS.map(({ key }) => pct(slot[key] as number, fblockSize))
        const leftOffsets = prefixOffsets(leftWidths)
        const rightWidths = RIGHT_SECTIONS.map(({ key }) => pct(slot[key] as number, fblockSize))
        const rightOffsets = suffixOffsets(rightWidths)
        return (
          <div className="pool-status-row" data-testid="pool-status-row" key={i}>
            <span
              className={`pool-slot-square state-${slot.state}`}
              title={slot.has_index ? `#${slot.index} ${slot.state}` : slot.state}
            />
            <div className="pool-section-bar">
              {LEFT_SECTIONS.map(({ key, cls }, idx) => (
                <span
                  key={key}
                  className={`pool-section ${cls}`}
                  style={{ left: `${leftOffsets[idx]}%`, width: `${leftWidths[idx]}%` }}
                />
              ))}
              {RIGHT_SECTIONS.map(({ key, cls }, idx) => (
                <span
                  key={key}
                  className={`pool-section ${cls}`}
                  style={{ right: `${rightOffsets[idx]}%`, width: `${rightWidths[idx]}%` }}
                />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}
