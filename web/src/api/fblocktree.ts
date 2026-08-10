const BASE = '/api/farcd'

// Mirrors internal/api/treejson.go's treeNodeJSON. Value is whatever
// decodeScalarValue produced: a plain number for uint8/uint32/int32/float32/
// float64, or a decimal string for uint64/int64/timestamp/duration (those
// can exceed Number.MAX_SAFE_INTEGER -- see web/src/api/ns.ts's own note on
// why farcd sends ns-scale integers as strings). Size (bytes) is always a
// plain number -- well within safe-integer range even for a whole fblock.
export type TreeNode = {
  id: number
  role: string
  type: string
  parent: number
  value?: string | number
  size?: number
  child_count: number
}

export type TreeLevel = {
  node: TreeNode
  children: TreeNode[]
  offset: number
  total: number
}

async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

// getFblockTreeNode fetches one level of a finalized fcontainer's media
// tree: nodeId's own decoded fields plus a page of its immediate children
// (GET /storages/{id}/fcontainers/{uuid}/tree, internal/api/treejson.go).
// Omitting nodeId fetches the root. A "frames(video)"/"frames(audio)"
// container can have millions of children, hence offset/limit -- the
// fblock-status page pages through them rather than requesting them all.
export async function getFblockTreeNode(
  storage: string,
  uuid: string,
  nodeId?: number,
  opts?: { offset?: number; limit?: number },
): Promise<TreeLevel> {
  const params = new URLSearchParams()
  if (nodeId !== undefined) params.set('node', String(nodeId))
  if (opts?.offset !== undefined) params.set('offset', String(opts.offset))
  if (opts?.limit !== undefined) params.set('limit', String(opts.limit))
  const qs = params.toString()
  const url = `${BASE}/storages/${storage}/fcontainers/${uuid}/tree${qs ? `?${qs}` : ''}`
  return (await ok(await fetch(url))).json()
}
