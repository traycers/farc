# internal/storage: real buffer pool, early index assignment, ADR-017 periodic flush

Status: resolved

## Problem

`docs/docs/archive/00-requirements.md` §4.7-4.8 documents a real handshake
between the Storage library and its consumer: "get a buffer" (returns the
buffer, its max size, and current backpressure status), "fill it", "return
the completed buffer" (queued FIFO for write) — and ADR-017 documents a
periodic partial-flush mechanism (write whatever's `ready_bytes` every time
`fchunk_size` is reached or a timeout `T` elapses, so an open fcontainer's
data isn't only in memory until it closes).

Neither is actually implemented. Today:

- `internal/ingest`'s `Recorder` interface has exactly one method,
  `WriteFcontainer(channels, begin, end, filler, now)` — no "get a buffer"
  call at all. The consumer just allocates a fresh `*fcontainer.Filler`
  itself (`fcontainer.New()`) and only talks to `internal/storage` once,
  at the very end, when the filler is already fully populated.
- `internal/storage/recorder.go`'s `WriteFcontainer` only then calls
  `u.mgr.SelectNextIndex`/`BeginWrite` — i.e. **a physical index is chosen,
  and the fblock only becomes `in_progress`, after the entire fcontainer has
  already been filled in memory.** ADR-017's own "Открытые вопросы" section
  already names this as unresolved: "Момент выбора физического индекса N...
  этим ADR не определён" — without an earlier index, partial chunks ready
  before the container closes have nowhere physical to be written.
- `recorder.go`'s own doc comment says as much: "ADR-017's incremental
  flush is not implemented — filler must already be fully populated (its
  fcontainer closed) before this call."

**Observed symptom** (2026-08-13, via the fblocks-status-grid admin page
added this session): the `in_progress` (blue) state visibly flickers —
barely visible before flipping to `ready` (green). Root cause: `in_progress`
today only spans the physical disk-write duration (bounded by drive
throughput, seconds), never the actual in-memory fill duration (which can
be minutes to hours for a continuous channel) — because the fblock has no
identity/index at all until filling is already done.

## Design (settled via `/mattpocock-skills:grilling`, 2026-08-13)

- **Pool shape.** Several in-memory `*fcontainer.Filler` objects can exist
  at once (so a new one can start filling while the previous one is still
  being written to disk — the existing "fill next while writing current"
  overlap), but **only one holds an assigned physical index/`in_progress`
  state at any moment**: the one actually at the head of the write queue,
  currently streaming bytes to disk right now.
- **When the index is assigned.** Right before a buffer is about to
  actually start being written — i.e. `SelectNextIndex`/`BeginWrite` moves
  from "only called inside `WriteFcontainer`, after the buffer is fully
  returned" to "called when the buffer is handed the write slot," which in
  the steady-state (no backlog) case coincides with fill-start, since
  there's nothing else ahead of it in the queue. Under backlog (filling
  outpaces writing), additional fully-filled buffers simply queue up in
  memory **without any physical index at all**, waiting their FIFO turn;
  only the one at the head gets a number.
- **ADR-017's `T`-timeout only applies when fill and write coincide** (no
  backlog — the buffer currently being filled is the same one currently
  being written). Under backlog, the timeout is ignored entirely: write the
  current in-progress buffer as fast as possible to drain the backlog,
  rather than continuing to pace small increments by a timer whose original
  purpose (bounding unwritten-data risk) no longer matters once there's
  already a queue of complete, unwritten buffers sitting in memory anyway.
- **fblocks-status-grid page (web) implication:** a queued, fully-filled,
  index-less buffer has no physical position to render on the existing
  0..N-1 grid. It's shown as an extra "?" square, appended outside the
  indexed grid, one per currently-queued index-less buffer — not invisible,
  and not shoehorned onto an existing physical square.
- **Backpressure is a single signal, not two.** No separate `StorageEngine`-
  queue-depth signal to reconcile with the pool — there is conceptually one
  fixed-size pool of M buffer slots (filling, queued-waiting-to-write, or
  actively being written all count as "occupied"); backpressure is purely
  occupied-count N against configured total M. `StorageEngine.EnqueueWrite`'s
  own internal FIFO is just an implementation detail inside the "occupied"
  state, not a second, separate thing to also track. N/M's actual threshold
  values (matching the existing NORMAL/WARNING/BACKPRESSURE levels) are
  operator-configured via env, per this codebase's existing tuning-param
  convention (`internal/config`-style env vars, not the JSON topology file).

### Crash recovery for a partially-written `in_progress` fblock

ADR-017 itself leaves this open ("Восстановление после сбоя во время
частичной записи... не рассмотрено") and its own promise ("теряется только
последний неполный фчанк, а не весь фконтейнер") doesn't actually hold
without one: there's no TOC/epilogue until close, so without something
extra, a crash before close still loses the whole open fcontainer — and
now that a shared Filler can stay open far longer, covering several
channels at once, that loss is more consequential than before.

Decided (2026-08-13): on process restart, attempt real recovery of
whichever one fblock was `in_progress` at crash time — reconstruct a valid
TOC from the already-written raw content and promote it all the way to
`ready` (with an `end` truncated to whatever was actually recoverable),
not just a safe "mark it `bad` and move on."

Mechanism — a **combined data+magic write**, replacing ADR-017's plain
`writable` write:

- Every periodic-flush trigger (`fchunk_size` reached or timeout `T`, in
  the no-backlog case only — see below) writes, **as one single
  write-verified operation**: `writable` bytes of real content, followed
  immediately by one alignment-unit-sized (4 KiB) magic-number trailer.
  Both succeed or both don't — there is no window where one lands without
  the other.
- The next trigger overwrites that trailer with the next batch of real
  content, then writes its own new trailer past that — the magic always
  marks "the current live end of the content stream," continuously
  relocated forward, never accumulating.
- **On crash, at most the single in-flight combined write is lost** — every
  earlier trigger's write was already independently confirmed (write-verify
  per ADR-005) together with its own trailer, so recovery just needs to
  find the last successfully-written trailer and treat everything before
  it as trustworthy.
- **Recovery algorithm** (Storage startup, extending `ConsistencyCheck`):
  for whichever fblock is `in_progress` at start, walk the raw content
  bytes from offset 0, decoding `mediatree.Element`s one at a time
  (mirroring `mediatree.DecodeContentWithOffsets`) until hitting the magic
  trailer (clean, expected stop) or an element that fails to decode
  (unexpected — but bounded to at most one alignment unit of ambiguity,
  since every fully-confirmed prior write already carries a verified
  trailer). Build a TOC (`toc.Build`) from whatever decoded cleanly, write
  it plus a proper epilogue, and run `CompleteWrite` with `end` = the last
  successfully-recovered frame's time (earlier than originally intended,
  never later).
- **No format change** to `05-data-format.md`'s per-node encoding (the
  earlier idea of a magic after every node was considered and rejected —
  format-version risk and permanent per-frame storage overhead, for no
  benefit beyond what the trailing, transient, content-area-only magic
  already gives). No header/catalog layout change either (the even-earlier
  idea of a progressively-rewritten `ready_bytes` catalog counter,
  requiring the header region to be padded to a 4 KiB multiple and
  rewritten on every trigger, was superseded by this — simpler, and avoids
  touching `04-storage-operations.md`'s `min_container_share` capacity
  formula at all).
- Under backlog (filling outpaces writing — see "Pool shape" above),
  timeout-triggered writes are skipped entirely in favor of writing as fast
  as possible; the magic-trailer mechanism still applies to whichever
  writes do happen, just paced by `fchunk_size` instead of `T`.

### Documentation deliverables (part of this ticket, not just code)

- Revise ADR-017 (already revised four times per its own header — this is
  a fifth) to specify the combined data+magic write and the recovery
  algorithm, resolving its own "Восстановление после сбоя" open question.
- `03-storage-format.md`: document the magic-trailer convention as a
  content-area, `in_progress`-only construct (never present in a closed,
  `ready` fblock).
- No change needed to `04-storage-operations.md`'s capacity/
  `min_container_share` formula — confirmed unaffected, since the trailer
  is transient and doesn't reduce a closed fblock's final content capacity.

## Scope (implementation-level, for `/plan`)

- Pool size (how many buffers can be filling/queued at once before
  backpressure kicks in), `T`'s default value/range, and where `T` lives in
  config (ADR-017 already flags both as open — `03-storage-format.md`
  neighbor of `fchunk_size`).
- The concrete new API shape replacing today's single-method `Recorder`
  interface (something like "request a buffer" / "return a filled buffer",
  per `00-requirements.md` §4.8's cycle) — `internal/ingest` is this API's
  only caller today, but the new shape belongs in `internal/storage`.
- How `EnqueueWrite`'s existing `StorageEngine`-level FIFO (which already
  only ever handles fully-index-assigned, ready-to-write buffers) relates
  to this new pre-index queue of filled-but-unassigned buffers — likely a
  new, smaller queue in front of it, not a replacement.
- Live WS surface for the fblocks-grid page's "?" indicator (today's
  `catalog`/live-tree endpoints have no notion of "queued, unassigned"
  buffers at all yet).
- The existing `BackpressureSignal` (`internal/farcd` wiring
  `StorageEngine.Level()` into `ChannelIngest`, per ADR-011) is superseded
  by the pool-occupancy signal above — needs rewiring to source from the
  new pool's N/M count instead of `StorageEngine.Level()` directly.

Rollout: **no flag** — this replaces the internal write-buffer mechanism
without changing observable behavior for any existing caller (`fblock.
write.started`/`.completed` events still fire at the same conceptual
moments, just correctly timed relative to actual filling now, plus new
backpressure signaling per §4.7 that consumers can choose to ignore).

Unblocks `02-ingest-shared-filler-per-storage.md` — that ticket calls the
API this one builds.

## Comments

Implemented (2026-08-14): `internal/storage/pool.go` (`Pool`, `PoolStatus`,
`PoolTuning`), `internal/storage/segment.go` (`Segment` interface,
`segmentImpl`, `Unit.BeginSegment`, magic-trailer promotion/close/retry
logic), `internal/storageengine/engine.go` (`EnqueueOpenWrite`/
`WriteHandle`, timer-bounded `Run`), `fblock/trailer.go`
(`EncodeTrailer`/`FindTrailer`, `MagicTrailer`), `mediatree/partial.go`
(`DecodeContentPartial`), crash recovery in
`internal/storage/consistency.go` (`recoverPartialWrite`), config wiring
(`internal/config`'s `FARC_STORAGE_POOL_*` env vars,
`fblock.Params.FlushTimeoutNS`), and `BackpressureSignal` rewired to
`unit.PoolStatus()` in both `internal/farcd/farcd.go` and
`internal/api/channels.go`. `WriteFcontainer` kept as a thin backward-compat
wrapper over `BeginSegment`/`Close` — no flag, existing tests (including the
corruption-retry test, adapted to the new chunk-boundary reality) pass
unchanged. Full test suite green (including `-race` on `internal/storage`
and `internal/storageengine`), `golangci-lint run` clean. Docs updated:
ADR-017 (5th revision), `03-storage-format.md` (§5.2/§8.1.1), `CONTEXT.md`
(new **Pool** entry).
