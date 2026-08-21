# Grid keeps sizing for a deleted channel's stale checked id

Status: fixed (2026-08-21, via TDD)

## Problem

Reported 2026-08-21: after deleting a channel, the Live page's video grid
kept the layout sized for 3 channels even though only 2 remain.

## Root cause

`LivePage.tsx`'s checked-channel set is persisted in `localStorage` and
restored on every mount, but it was never reconciled against the actual
list of channels `listChannels()` returns. A channel removed elsewhere
(`ChannelsIndexPage`) leaves its id in the stored checked set; on the
next Live page visit, `channelIds` (built straight from `checked`) still
includes it, so `VideoGrid`/`layoutCells` sizes the grid for one more
channel than actually exists — an empty cell where the deleted channel's
tile used to be.

## Fix

Once `listChannels()` resolves, `LivePage.tsx` prunes `checked` down to
the ids actually present in the fetched list (updating both state and
`localStorage`), so a deleted channel's id can't outlive the channel
itself. This mount-time fetch was already the page's only channel-list
resync point (per `issues/02`'s scope boundary — no live add/remove
handling), so this fix only needed to hook into it, not add a new one.

## Comments

Covered by a new `LivePage.test.tsx` case: seeds `localStorage` with 3
checked ids, mocks `listChannels` to return only 2 of them, and asserts
the grid renders exactly 2 tiles (no stale empty cell) and that
`localStorage` itself gets pruned to match. `npm test` 173/173, `npm run
build` clean, rebuilt and redeployed to the reporting user's live stack.
