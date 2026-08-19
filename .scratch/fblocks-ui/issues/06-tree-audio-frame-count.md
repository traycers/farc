# Fblock tree should always show a frame count for audio, like it does for video

Status: resolved (2026-08-18) — not a bug, see Investigation

## Problem

Reported 2026-08-17, from a tree copied into the (since-reverted) repo-root
`TODO.md` after stopping recording on all channels. Video's frame group
always renders as a single summarized node, e.g.:

```
frames(video) (void) [-]
└── frame(video) (group) = 654 frames [+]
```

Audio's equivalent node exists and can show the same thing (seen for
channels 1/3/5's last config in the copied tree — `frame(audio) (group) =
641 frames`), but only when `frames(audio)` is manually expanded (`[-]`).
For every other case in the same tree (channels 1/2/4, most configs),
`frames(audio) (void) [+]` is shown collapsed, with no frame count visible
without expanding it — unlike video, where every `frames(video)` node in
the same tree happened to be expanded.

## Investigation

`web/src/components/FblockTree.tsx`'s `defaultOpen(node, depth)` decides
default-expand purely from `depth < DEFAULT_OPEN_DEPTH (5)` and
`node.type !== 'group'` — no role check anywhere. `frames(video)` and
`frames(audio)` are both children of their respective `config(...)` node,
so both sit at the identical depth (8) in the tree, and are handled by
`defaultOpen` completely symmetrically. `web/src/api/frameGrouping.ts`'s
per-role grouping thresholds (`frame(video)`: 100, `frame(audio)`: 500 —
a deliberate 2026-08-14 grilling decision for typical frame-count
magnitude) don't affect this either: they decide whether a run of frame
nodes collapses into one synthetic `group` node, not whether the parent
`frames(...)` node itself starts open or closed.

No page passes any role-based override into `FblockTree` (only consumer:
`FblockTreePage.tsx`).

**Conclusion**: not a bug. The reported tree (`frames(video)` open,
`frames(audio)` closed for most channels) reflects that specific UI
session's manual click history (`FblockTree`'s own `manual` per-node-id
map, which always overrides the depth-based default) — the user had
clicked open every video frames-group but not every audio one — not an
asymmetry in default-expand behavior, which treats both kinds identically.
No code change made.
