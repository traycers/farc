// Mirrors fblock's fixed-format constants -- pure format-level numbers
// (fblock/geometry.go's FixedOverheadBytes/HeaderChecksumsSize,
// fblock/prolog.go's FixedPrologSize, fblock/epilog.go's EpilogSize), never
// vary per deployment, unlike catalog_size below (which depends on this
// storage's own Geometry). Params' own JSON size is deliberately not
// accounted for here -- a few dozen bytes, invisible at real FblockSize
// scale, and not worth a backend round trip for this visualization.
const PROLOG_FIXED = 56
const MAGIC_LABEL = 8 // magic_catalog / magic_content / magic_toc, each 8 bytes
const HEADER_CHECKSUMS = 12
const EPILOG_SIZE = 20

// Real fblocks are MB-GB; prolog/catalog/toc/epilog are tens to low
// thousands of bytes -- their true proportion rounds to 0 rendered pixels,
// so each gets this fixed minimum width instead (a landmark, not a scale
// model) while content/free split the true remaining space by real byte
// ratio (see the flexGrow math below).
const LANDMARK_WIDTH = '10px'

// catalogSize mirrors fblock.CatalogSize(maxChannels, n) -- computable
// client-side since Geometry (MaxChannels, N) is already known.
function catalogSize(maxChannels: number, n: number): number {
  const entrySize = 33 + Math.ceil(maxChannels / 8)
  return maxChannels * 2 + n * entrySize
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

export type FblockFillBarProps = {
  fblockSize: number
  maxChannels: number
  catalogN: number
  contentBytes: number
  estimatedTocBytes: number
}

// FblockFillBar renders one fblock's on-disk byte layout as a Bootstrap
// multi-segment progress bar: prolog/catalog/content grow from the left
// (matching write order -- prologue and catalog are laid out first), toc/
// epilog are pinned to the right (matching how they're actually located --
// from the end of the fblock backward, see CLAUDE.md's "Read from both
// ends"). prolog/catalog/epilog render at a fixed minimum width (see
// LANDMARK_WIDTH) since their true byte proportion is invisible at real
// FblockSize scale and never changes during a recording. toc keeps that
// same minimum width as a floor but also grows via flexGrow as
// estimatedTocBytes increases -- unlike prolog/catalog/epilog, its size
// is not fixed: it's an estimate of what the TOC will cost once the fblock
// is finalized, and needs to visibly grow so the bar can be read as "how
// close is this fblock to being done." content and the free remainder
// split the true remaining space via flexGrow, proportional to their real
// byte ratio, so that part of the bar stays an accurate fill gauge.
export default function FblockFillBar({ fblockSize, maxChannels, catalogN, contentBytes, estimatedTocBytes }: FblockFillBarProps) {
  const prolog = PROLOG_FIXED
  const catalog = MAGIC_LABEL + catalogSize(maxChannels, catalogN) + HEADER_CHECKSUMS + MAGIC_LABEL
  const toc = MAGIC_LABEL + estimatedTocBytes
  const epilog = EPILOG_SIZE

  const freeBytes = Math.max(0, fblockSize - prolog - catalog - contentBytes - toc - epilog)

  return (
    <div className="mb-3">
      <div className="progress" style={{ height: '1.5rem' }} role="progressbar">
        <div
          className="progress-bar bg-secondary flex-grow-0"
          style={{ flexBasis: LANDMARK_WIDTH, flexShrink: 0 }}
          title={`prolog: ${formatBytes(prolog)}`}
        />
        <div
          className="progress-bar bg-info flex-grow-0"
          style={{ flexBasis: LANDMARK_WIDTH, flexShrink: 0 }}
          title={`catalog: ${formatBytes(catalog)}`}
        />
        <div
          className="progress-bar bg-primary"
          style={{ flexGrow: contentBytes, flexBasis: 0, flexShrink: 1, minWidth: 0 }}
          title={`данные: ${formatBytes(contentBytes)}`}
        />
        <div
          className="progress-bar bg-transparent"
          style={{ flexGrow: freeBytes || 0.0001, flexBasis: 0, flexShrink: 1, minWidth: 0 }}
          title={`свободно: ${formatBytes(freeBytes)}`}
        />
        <div
          className="progress-bar bg-warning"
          style={{ flexBasis: LANDMARK_WIDTH, flexGrow: estimatedTocBytes, flexShrink: 1, minWidth: 0 }}
          title={`toc (оценка): ${formatBytes(toc)}`}
        />
        <div
          className="progress-bar bg-danger flex-grow-0"
          style={{ flexBasis: LANDMARK_WIDTH, flexShrink: 0 }}
          title={`эпилог: ${formatBytes(epilog)}`}
        />
      </div>
      <div className="d-flex justify-content-between small text-muted mt-1">
        <span>
          prolog {formatBytes(prolog)} · catalog {formatBytes(catalog)} · данные {formatBytes(contentBytes)}
        </span>
        <span>
          toc (оценка) {formatBytes(toc)} · эпилог {formatBytes(epilog)}
        </span>
      </div>
    </div>
  )
}
