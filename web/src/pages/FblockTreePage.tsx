import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { getFblockInfo, getFblockTocRows, subscribeFblockLiveTocRows, type FblockInfo, type TreeNode } from '../api/fblockTree'
import { tocRowsToTree } from '../api/tocRowsToTree'
import { nsToDisplayString, nsToDisplayStringPrecise } from '../api/ns'
import { groupFrameNodes } from '../api/frameGrouping'
import { formatDecodedValue } from './fblockTreeFormat'
import { renderTreeAsText } from './fblockTreeText'
import { downloadTextFile } from '../lib/download'
import FblockTree from '../components/FblockTree'

export function formatValue(node: TreeNode): string | undefined {
  if (node.type === 'timestamp' && node.value) return nsToDisplayStringPrecise(BigInt(node.value))
  if (node.value !== undefined) return formatDecodedValue(node.role, node.value)
  return undefined
}

// FblockTreePage shows one fblock's tree -- either static (ready, from its
// stored TOC) or live (in_progress, streamed over WS from whichever
// segment is currently shared by every channel of this Storage). Frame
// nodes are grouped client-side (frameGrouping.ts) before rendering, and
// FblockTree itself handles collapse/expand (default depth 5).
export default function FblockTreePage() {
  const { id = '', index = '' } = useParams<{ id: string; index: string }>()
  const navigate = useNavigate()
  const [info, setInfo] = useState<FblockInfo | null>(null)
  const [tree, setTree] = useState<TreeNode | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setError(null)
    setInfo(null)
    setTree(null)
    getFblockInfo(id, Number(index))
      .then((i) => {
        setInfo(i)
        if (i.state === 'ready') {
          if (!i.uuid) throw new Error(`fblock ${index} is ready but has no uuid`)
          return getFblockTocRows(id, i.uuid).then((rows) => setTree(tocRowsToTree(rows)))
        }
        if (i.state === 'in_progress') {
          return undefined // live subscription effect below handles this
        }
        throw new Error(`fblock ${index} is not viewable (state=${i.state})`)
      })
      .catch((e) => setError(String(e)))
  }, [id, index])

  useEffect(() => {
    if (info?.state !== 'in_progress') return
    setTree(null)
    const unsubscribe = subscribeFblockLiveTocRows(id, Number(index), (msg) => setTree(msg.rows ? tocRowsToTree(msg.rows) : null), setConnected)
    return unsubscribe
  }, [id, index, info?.state])

  const displayTree = tree ? groupFrameNodes(tree) : tree

  return (
    <section>
      <div className="d-flex justify-content-between align-items-center mb-3">
        <h1 className="mb-0">
          Fblock #{index} <small className="text-body-secondary">({id})</small>
        </h1>
        <div className="d-flex gap-2">
          <Link to={`/storages/${id}/fblocks/${index}/tree/toc`} className="btn btn-outline-secondary">
            TOC table
          </Link>
          {tree && (
            <button
              type="button"
              className="btn btn-outline-secondary"
              onClick={() => downloadTextFile(`fblock-${info?.uuid ?? index}-tree.txt`, renderTreeAsText(tree, formatValue))}
            >
              Download tree (.txt)
            </button>
          )}
          <button type="button" className="btn btn-outline-secondary" onClick={() => navigate(-1)}>
            Back
          </button>
        </div>
      </div>
      {error && <div className="alert alert-danger">{error}</div>}
      {info && (
        <div className="mb-3 d-flex gap-3 align-items-center">
          <span className="badge text-bg-secondary">{info.state}</span>
          {info.protected && <span className="badge text-bg-info">protected</span>}
          {info.state === 'in_progress' && (
            <span className={`badge ${connected ? 'text-bg-success' : 'text-bg-warning'}`}>{connected ? 'live' : 'reconnecting…'}</span>
          )}
          {info.uuid && <span>uuid: {info.uuid}</span>}
          {info.begin && <span>begin: {nsToDisplayString(BigInt(info.begin))}</span>}
          {info.end && <span>end: {nsToDisplayString(BigInt(info.end))}</span>}
        </div>
      )}
      <div className="border rounded p-3 bg-body-tertiary">
        <FblockTree root={displayTree} formatValue={formatValue} />
      </div>
    </section>
  )
}
