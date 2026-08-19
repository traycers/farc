# internal/ingest: route every channel of a Storage into one shared Filler

Status: resolved
Blocked by: 01

## Problem

`internal/ingest` today creates one `CapturePolicy` per channel
(`NewCapturePolicy(channel, recorder, ...)`), and each `CapturePolicy` owns
its own `*fcontainer.Filler`, created independently in `openSegmentLocked`
(`p.filler = fcontainer.New()`). Every channel's `closeSegmentLocked` then
calls `Recorder.WriteFcontainer` with a single-element channel slice
(`internal/ingest/policy.go:251`: `p.recorder.WriteFcontainer([]uint16{p.channel}, ...)`).

The practical effect: **no two channels ever share a fcontainer**. Each
channel's recording produces its own separate fblock, always.

This directly contradicts the documented model:

- `docs/docs/archive/00-requirements.md`: "Один фконтейнер может содержать
  данные нескольких каналов одновременно, вплоть до постоянной записи всех
  каналов сразу — обычный, а не крайний режим эксплуатации."
- `docs/docs/archive/adr/014-channel-registry.md` makes the same point
  while explaining why the catalog can't index fblocks by channel.
- `docs/docs/archive/07-media-tree.md` §2's tree hierarchy is built for it:
  `root → channels → channel (×N)`.
- `internal/fcontainer.Filler` itself already supports multiple channels
  — `AddStreamParams(channel, stream, kind, params)` takes `channel` as a
  parameter and creates a new `channel` node on first use for that number;
  it has never been a one-channel-only type. Only `internal/ingest`'s
  wiring (one `Filler` instance per `CapturePolicy`) limits it to one
  channel in practice.
- The lower layers already support it: `storage.Unit.WriteFcontainer`'s
  own signature takes `channels []uint16` (plural), and `internal/storage`
  already has a per-fblock, multi-channel `channel_bitmap`
  (`RegisterChannels`/`SetChannelBit`/`ChannelBit`).

Confirmed by the user (2026-08-13) as a critical bug, not a stylistic gap.

(Side note: this session also found and fixed `CONTEXT.md`'s own
`fcontainer` glossary entry, which used to incorrectly say "for one
channel" — the doc mismatch went both ways, code and glossary alike, but
only the glossary has been corrected so far.)

## Why this matters

- **Storage efficiency.** Several low-bitrate channels each claiming a
  whole dedicated fblock (padded to `min_container_share` etc.) wastes
  space the documented normal mode assumes won't be wasted.
- **This session's fblock-live work exists only because of this gap.** The
  storage-scoped live-tree admin page (`internal/api/fblocktree.go`'s
  `mergeLiveTrees`/`handleStorageLiveTreeWS`) has to reassemble multiple
  channels' *separate* live trees into one combined view purely for
  display, precisely because there's no real shared fcontainer to read
  from. This ticket landing is expected to simplify or obsolete that
  reassembly code — it's currently working around the symptom, not the
  cause.
- **Downstream consumers already assume the multi-channel case is real.**
  `internal/vaablocks`/`internal/msmd` derive "per-channel video-only
  vaa-blocks" from one fblock's TOC specifically because a TOC can contain
  several channels' data at once; `internal/api/eventpush.go`'s
  `matchesSubscription` filters fblocks by channel via `channel_bitmap`
  for the same reason. The write path is the one place in the system that
  doesn't yet produce what everything above it already expects to handle.
  A read-side audit (2026-08-13, background research) confirmed
  `handleCandidates`, `LatestReadyForChannel`, `vaablocks.Compute`,
  `matchesSubscription`, `tocindex`, and `segmentcache` are all already
  written generically around `channel_bitmap`/per-channel TOC subtrees —
  none of them need changes for this.

## Design (settled via `/mattpocock-skills:grilling`, 2026-08-13)

- **Grouping.** All channels of a Storage always share exactly one
  `Filler` — not a configurable/partial grouping. (Justification surfaced
  during grilling: channels are already grouped into the same Storage
  specifically because they share the same retention requirement, so "all
  channels of a Storage" is already a meaningful, pre-existing grouping
  boundary, not an arbitrary one.)
- **Structure.** `CapturePolicy` stays exactly as it is today in
  responsibility — one instance per channel, still owning policy type
  (continuous/event), prerecord/postrecord windows, triggers, and its own
  `FrameQueue`. What changes: it no longer owns/creates its own `Filler`.
  Instead, there is one shared `Filler` per Storage that every
  `CapturePolicy` of that Storage's channels writes into (via whatever API
  `01-storage-buffer-pool-and-early-index-assignment.md` produces).
- **Mixed policy types.** Continuous and event-policy channels can and do
  share the same Filler simultaneously — no restriction to same-type-only
  grouping.
- **Join dynamics.** A channel starts contributing to whichever Filler is
  currently open/active for its Storage immediately, the moment its own
  `CapturePolicy` decides to let frames through (recording start, trigger
  fire, prerecord replay) — never waits for "the next rotation."
- **Close dynamics.** The shared Filler's lifecycle (when it closes and a
  new one opens) is driven **purely by byte-fullness** (ticket 01's
  mechanism), never by any individual channel's start/stop. A channel
  stopping recording does not close the shared Filler for the other
  channels still using it — it simply stops contributing frames to
  whichever Filler is currently open.
- **Prerecord replay across a Filler rotation.** Not an issue: replay just
  calls `AddFrames` with historical timestamps into whichever Filler is
  active at the moment the trigger fires. A channel/stream's own `frames`
  subtree only cares about its own relative (creation) order — TOC/
  candidates/vaablocks all read per-channel subtrees, never global
  cross-channel interleaving order — so which Filler generation a replay
  burst lands in doesn't affect correctness.
- **Retention.** Stays Storage-wide, unaffected — this was never
  per-channel even before this change, and multi-channel sharing doesn't
  newly require it to become one (explicitly decided: not in scope, and
  not a bug this introduces).
- **Concurrency.** The shared `Filler` is guarded by a single plain
  `sync.Mutex` (not a channel/actor pattern) — `AddFrames`/
  `AddStreamParams` are fast, purely in-memory append operations, so lock
  hold time is negligible even with many channels contending; a
  channel-based single-writer-goroutine design was considered and rejected
  — `AddStreamParams` returns a `configID` synchronously, which the caller
  needs immediately for the following `AddFrames`, so a channel-based
  design would need a request-response (reply-channel/future) pattern for
  no throughput benefit over a mutex, and would be a stylistic outlier vs.
  the rest of this codebase's synchronization (`CapturePolicy.mu`,
  `StorageRegistry`, `NotificationBus`, `IndexManager` — all plain
  `sync.Mutex`/`RWMutex`).
- **Channel moved to a different Storage.** No special handling needed —
  falls out of decisions already made. `PUT /channels/{id}` changing
  `storage` already fully drains the old `ChannelIngest`/`CapturePolicy`
  and creates a new one (existing `handleUpdateChannel` behavior); the old
  Storage's shared Filler doesn't close just because one channel stopped
  writing to it (per "Close dynamics" above), and the new `CapturePolicy`
  simply joins whatever's currently open on the new Storage (per "Join
  dynamics" above).

Rollout: **no flag** — unconditional cutover for every Storage once this
lands.

## Scope (implementation-level, for `/plan`)

- The concrete shape of "one shared Filler per Storage that N
  `CapturePolicy`s can address" — e.g. does `IngestManager` own it
  directly, or a new small per-Storage coordinator type; how a
  `CapturePolicy.HandleFrame` call reaches "whichever Filler is currently
  active" (a reference that can change out from under it whenever a
  rotation happens, independent of that particular channel's own state) —
  and where the new mutex from "Concurrency" above actually lives.
- Whatever new tests this needs at the `CapturePolicy`/`IngestManager`
  seam — depends on the concrete shape above, to be worked out in
  `/plan`/TDD rather than prescribed here.
- Fallout cleanup for `internal/api/fblocktree.go`'s `mergeLiveTrees`/
  `channelPreviousRef`/`handleStorageLiveTreeWS` once a real shared Filler
  exists to read from directly — likely simplifies, tracked here as a
  known follow-up, not designed yet.

## Comments

Implemented (2026-08-14): `internal/ingest/storagesegment.go`
(`SegmentBackend`, `StorageSegment` — transparent reopen-on-rotation via
`storage.ErrSegmentClosed`, per-generation join tracking), `livefilter.go`
(`filterChannelElements` — keeps `LiveSnapshot`'s per-channel contract, and
therefore `internal/api/fblocktree.go`, unchanged for now; the "Fallout
cleanup" item above stays a real, not-yet-done follow-up). `CapturePolicy`
no longer owns a private `*fcontainer.Filler` — `policy.go` rewritten
(`ensureConfigLocked`'s `segment.Generation()` mismatch detection handles
mid-recording rotation; `closeSegmentLocked` now only stops this channel's
own contribution, never touches the shared segment). `IngestManager` gained
`storageSegments map[string]*StorageSegment` + `segmentForLocked`, keyed by
`StorageID`, lazily created, no explicit teardown. `ChannelConfig.Recorder`
renamed to `SegmentBackend` throughout (`internal/farcd`, `internal/api`).
All existing tests migrated to the new `fakeUnderlyingSegment`/
`fakeSegmentBackend` fakes (two tests whose old assertions depended on
close-triggers-a-write adapted per the ticket's own prediction); new tests
added for actual multi-channel sharing, rotation, and a `-race`-clean
concurrent-two-channels test (`storagesegment_test.go`,
`policy_sharedsegment_test.go`). Full repo test suite green, `-race` clean
on `internal/ingest`, `golangci-lint run` clean. Docs: `CONTEXT.md`'s
**fcontainer** entry corrected (write path now does what the format always
supported); `10-capture-policy.md`/`11-service-composition.md` given
correction notes where they described the old one-Filler-per-channel model
as current.
