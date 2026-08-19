// Mirrors internal/api/eventpush.go's poolPushMessage/poolSlotMessage --
// the pool-status-list live feed (.scratch/fblocks-ui/issues/
// 04-pool-status-list-plan.md). Field names match the wire JSON verbatim
// (snake_case), same convention as CatalogEntry/TreeNode in api/fblockTree.ts.
export type PoolSlot = {
  state: 'free' | 'queued' | 'active' | 'closing'
  index?: number
  has_index?: boolean
  prolog_size: number
  catalog_size: number
  content_size: number
  toc_size: number
  epilog_size: number
}

export type PoolPushMessage = {
  type: 'pool'
  storage: string
  slots: PoolSlot[]
}
