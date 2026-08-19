export type GridShape = { rows: number; cols: number }

// gridShape is the fully-automatic layout rule from .scratch/
// player-redesign/spec.md: 1 -> 1x1, 2 -> 1x2 (an explicit exception, not
// folded into the sqrt rule below -- two channels side by side, not a 2x2
// grid with two empty cells), 3+ -> an NxN square, N=ceil(sqrt(count)).
export function gridShape(count: number): GridShape {
  if (count <= 1) return { rows: 1, cols: 1 }
  if (count === 2) return { rows: 1, cols: 2 }
  const n = Math.ceil(Math.sqrt(count))
  return { rows: n, cols: n }
}

// layoutCells places channelIds row-major into gridShape's cells, leaving
// unused trailing cells null -- the one function VideoGrid renders from.
export function layoutCells(channelIds: number[]): { shape: GridShape; cells: (number | null)[] } {
  const shape = gridShape(channelIds.length)
  const cells: (number | null)[] = new Array(shape.rows * shape.cols).fill(null)
  channelIds.forEach((id, i) => {
    cells[i] = id
  })
  return { shape, cells }
}
