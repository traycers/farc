# 02 — Block channel creation/move into a full storage (frontend + backend)

Status: resolved

## Task

Today nothing stops a channel from being created (or moved) into a storage
that's already at `geometry.MaxChannels` — see the spec's decision 2 for why
this issue's notion of "full" (current configured channel count vs.
`MaxChannels`) is deliberately simpler than, and different from, the
backend's existing lazy `ErrChannelRegistryFull` check
(`internal/index/channels.go:88-98`), which this issue does not touch.

### Backend (`internal/api/channels.go`)

1. Add a small helper counting currently-configured channels on a storage,
   e.g.:
   ```go
   func (s *HttpApiServer) channelCountForStorage(storageID string) int {
       count := 0
       for _, c := range s.ing.List() {
           if c.StorageID == storageID {
               count++
           }
       }
       return count
   }
   ```
   (`IngestManager.List()` is `internal/ingest/ingestmanager.go:218`,
   `ChannelInfo.StorageID` is `internal/ingest/ingestmanager.go:60`; there's
   no existing per-storage count method to reuse.)
2. `createChannel` (lines 340-373): right after the existing storage lookup
   (`unit, ok := s.reg.Get(req.Storage)`, lines 347-350), reject with
   `409 Conflict` if `channelCountForStorage(req.Storage) >= int(unit.Geometry().MaxChannels)`
   (`Unit.Geometry()` is `internal/storage/unit.go:79`,
   `Geometry.MaxChannels` is `internal/storage/geometry.go:12-16`, type
   `uint16`). Message e.g.
   `fmt.Errorf("api: storage %q is full (max %d channels)", req.Storage, unit.Geometry().MaxChannels)`,
   same inline-`fmt.Errorf` style as the existing "unknown storage" check
   two lines above — no new package-level `errX` var needed.
3. `handleUpdateChannel` (lines 410-464): it already calls
   `s.ing.StorageOf(channel)` at line 438 purely for the 404 existence
   check, discarding the returned storage id (`if _, ok := ...`). Capture it
   instead (`oldStorageID, ok := s.ing.StorageOf(channel)`,
   `StorageOf` is `internal/ingest/ingestmanager.go:248`), and only run the
   same capacity check as step 2 when `oldStorageID != req.Storage` — i.e.
   skip the check entirely when the update leaves the channel on its
   current storage (spec decision 3: no self-exclusion bookkeeping needed,
   since a same-storage update was never going to add a new occupant).

### Frontend

4. `ChannelsIndexPage.tsx`: compute
   `channels.filter((c) => c.storage === storage).length` against the
   selected storage's `geometry.MaxChannels` (both already in state — no new
   fetch). When full, render a disabled `<button type="button" disabled title="...">New channel</button>`
   in place of the `<Link to="new">` (lines 115-117) — `Link` doesn't support
   a `disabled` prop since it renders an `<a>`. Tooltip text e.g. `` `Storage full (${count}/${max} channels)` ``.
5. `ChannelNewPage.tsx`: it already fetches both `listStorages()` and
   `listChannels()` (lines 28-38, currently only using the latter to compute
   the next channel id). Reuse that data to mark full storages: in the
   storage `<select>` (lines 104-110 in the version with the RTSP generate
   button already added), give each full storage's `<option>` a `disabled`
   attribute and append fullness to its label, e.g.
   `` {s.name || s.id}{isFull(s.id) ? ' (full, X/Y)' : ''} ``. Also disable
   the submit button whenever the *currently selected* storage is full (this
   also covers a full storage arriving pre-selected via
   `.scratch/channels-new-default-storage`'s `?storage=` param).
6. `ChannelEditPage.tsx`: same `<select>` treatment as step 5, with one
   exception — track the channel's *original* storage separately from the
   editable `storage` state (set once from `c.storage` alongside the
   existing `setStorage(c.storage)` at line 32) and never mark it `disabled`
   or count it against the submit-button block, no matter how full it is.
   Disable "Save changes" only when the *currently selected* storage differs
   from the original *and* is full — mirroring the backend's
   same-storage-skips-the-check rule from step 3.

## Tests

Backend (`internal/api/channels_test.go` or wherever `createChannel`/
`handleUpdateChannel` are already tested): a storage at `MaxChannels`
capacity rejects a new channel with 409; an update that keeps a channel on
its current (full) storage still succeeds; an update that moves a channel
to a different, full storage is rejected with 409.

Frontend (Vitest + RTL, following this session's `ChannelNewPage.test.tsx`
pattern): "New channel" button/link is disabled with the right tooltip when
the filtered storage is full on `ChannelsIndexPage`; a full storage's
`<option>` is `disabled` and labeled on `ChannelNewPage`/`ChannelEditPage`;
`ChannelEditPage` never disables the channel's own original storage even
when full.

## Verify

`go test ./internal/api/...` and `cd web && npx tsc -b && npx vitest run`.

## Comments

2026-08-20: Implemented test-first. Backend (`internal/api/channels.go`):
added `channelCountForStorage`; `createChannel` and `handleUpdateChannel`
(only when `oldStorageID != req.Storage`, captured from the existing
`s.ing.StorageOf(channel)` call) reject with 409 when the destination is at
`MaxChannels`. Test support: `newTestUnitWithGeometry`/
`regTestUnitWithGeometry` added to `testutil_test.go`/`channels_test.go` for
a `MaxChannels: 1` storage; three new tests
(`TestHandleCreateChannel_FullStorageRejected`,
`TestHandleUpdateChannel_SameStorageSkipsCapacityCheck`,
`TestHandleUpdateChannel_DifferentFullStorageRejected`).

Frontend: `ChannelsIndexPage.tsx`'s "New channel" renders a disabled
`<button>` with a `title` when the filtered storage is full;
`ChannelNewPage.tsx`/`ChannelEditPage.tsx` mark full storages `disabled` in
the `<select>` with a "(full, X/Y)" label and disable their submit button
accordingly, with `ChannelEditPage.tsx` tracking a separate
`originalStorage` state so the channel's own current storage is never
treated as full for itself. New test files
`StoragesIndexPage.test.tsx`/`ChannelEditPage.test.tsx`, extended
`ChannelsIndexPage.test.tsx`/`ChannelNewPage.test.tsx`.
