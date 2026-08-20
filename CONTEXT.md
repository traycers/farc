# Archive

farc's domain: one archive system recording RTSP streams to disk and serving them back. Hierarchy: Archive → N Storage → fblock → fcontainer, with fchunk as an orthogonal write-verify mechanism at the physical IO layer.

## Language

### System

**Archive**:
The whole system — every service together (`farcd`, `hls_server`, the web console) plus the data they manage. Not just the `farcd` process.
_Avoid_: using it for a single Storage — see Storage's own `_Avoid_` entry.

**Потребитель (Consumer)**:
The process embedding the storage library — in this system, that's `farcd` and only `farcd` (`hls_server`/the web console talk to `farcd` over HTTP/WS, they don't embed the library). One Consumer may work with several independent Storages at once.
_Avoid_: conflating this with Archive — Archive is the whole system; Consumer is specifically the one process that calls into `internal/storage`.

**Video Gateway**:
An architectural role: prepares and delivers video to web clients, talking to `farcd` only through its external API (events/TOC over WebSocket push, reads) — no direct Storage access. `hls_server` is this system's one concrete implementation of the role (a sibling role, Timeline Service, is documented but not implemented here).

**Доставка без догона (Best-effort, no catch-up delivery)**:
The delivery policy for every live push feed in this system — farcd's own WS event feed to any subscriber. Events are sent as they happen; nothing is queued or persisted on the sending side, and a disconnect never triggers replay of what was missed — a reconnecting consumer just picks up wherever the live feed currently is. A deliberate simplicity choice for a first version, not a permanent guarantee.
_Avoid_: assuming any subscriber ends up with a complete history — always assume a reconnect can lose events.

### Storage & fblock

**Хранилище (Storage)**:
A file or disk partition — a container for media data, organized as a flat list of fblocks. One logical writer plus many concurrent readers.

**fblock**:
A self-contained, fixed-size archive unit covering one time window; readable without any other fblock or a global header. States: `uninitialized → in_progress → ready → bad`, plus an independent `protected` (read-only) modifier. Holds exactly one fcontainer.
Two independent identities layered on top of each other: a **number/offset** (`offset = index * block_size`) naming the fixed physical slot on disk — permanent, reused forever as the writer cycles through the Storage — and a **UUIDv4** naming the specific fblock (data + fcontainer) currently occupying that slot. Overwriting a slot doesn't reuse the old fblock: it deletes the fblock at that UUID and creates a new one, at the same number/offset.
_Avoid_: treating number/offset as identifying "the same fblock" over time — after an overwrite the number is unchanged but the fblock (by UUID) is a different one.

**fcontainer**:
One continuous recording (a segment) — its Content plus its TOC together, built from frames. Self-contained: interpretable without any other fcontainer and without Storage metadata. Always fits entirely inside a single fblock — never spans more than one. Can hold data for several channels at once: the tree itself is shaped for it (`root → channels → channel ×N`, `07-media-tree.md` §2), and "постоянная запись всех каналов сразу" (continuous recording of every channel at once) is the system's normal operating mode, not an edge case (`00-requirements.md`). This is now what the write side actually does: `internal/ingest`'s `StorageSegment` routes every channel of a Storage into the one shared `storage.Segment` the buffer pool currently has open for it (`internal/storage/segment.go`/`pool.go`) — `CapturePolicy` no longer owns a private Filler, only its own frame queue and recording state. The admin UI's old storage-scoped, whole-storage-merged live-tree page (and its `mergeLiveTrees`/`remapTreeIDs` reassembly logic) is gone — superseded by a per-fblock live endpoint (`internal/ingest.IngestManager.LiveTreeForStorage`, `internal/api/fblocktree.go`'s `handleFblockLiveTreeWS`) that reads this one shared tree directly, no per-channel reassembly needed. `CapturePolicy.LiveSnapshot`/`internal/ingest/livefilter.go`'s `filterChannelElements` (a per-channel subtree view) remain as `internal/ingest`'s own tested read API, independent of any specific admin page.
_Avoid_: assuming "one fcontainer = one channel" — that was true of an earlier version of this glossary entry itself (and of the write path before the multi-channel-fcontainer ticket landed); the format, the on-disk `channel_bitmap` (Compact position entry below), and now the write path itself have always/now all support several channels sharing one fcontainer.

**fchunk**:
The write-verify unit at the physical IO layer: the whole fblock buffer is written fchunk by fchunk, each read back and compared. Not a structural part of an fblock or fcontainer — just the write granularity (`fchunk_size`).
_Avoid_: treating it as a piece "inside" an fblock's content section — write-verify chunking spans the fblock's entire buffer (prologue through epilogue), not just its content.

**Каталог (Catalog)**:
A snapshot of every fblock's state in a Storage (channel registry + per-fblock state/uuid/begin/end/channel_bitmap), embedded in every single fblock's header. Mandatory, always present — this is what makes an fblock self-contained.
_Avoid_: assuming this is SSD-specific or optional — that's the External catalog, a different thing.

**Внешний каталог (External catalog)**:
An optional copy of the Catalog kept on a separate, faster disk (typically SSD) purely to skip a full header scan of the main Storage at startup (ADR-007). Not a source of truth — just a cache.

**write_sequence**:
A monotonic counter local to one Storage (not per-fblock, not process-global), incremented on every write attempt — including a retry after a failed/`bad` one. Its only purpose: letting a reopened Storage determine, among all its fblocks, which one carries the freshest embedded Catalog snapshot — the fblock with the highest validated `write_sequence` wins (ADR-008).

**StorageEngine**:
The sole owner of a Storage's disk I/O — every write and every read of that Storage's underlying file/partition goes through it, one at a time (ADR-005: writes have absolute priority). Internally queues pending fblock writes, so the next fblock can already be filled while the current one is still being written to disk.
_Avoid_: confusing this with **Pool** (below) — a real, separate `internal/storage.Pool` type now exists one layer above StorageEngine, managing which segment currently holds a physical index; StorageEngine's own internal write-queue is a lower-level, per-job mechanism `Pool` builds on, not the same thing.

**Pool**:
`internal/storage.Pool` — holds up to a configured number of segments (each an in-memory fcontainer being filled) that may be filling, fully-filled-but-unassigned ("queued"), or actively being written at once, but only the FIFO head ever holds a physical fblock index — everything else has no physical position yet (the admin "fblocks status" page's "?" square). Occupancy against operator-configured thresholds (`PoolTuning`) is the single backpressure signal (ADR-017's crash-recovery/early-index-assignment work), superseding StorageEngine's own write-queue depth for that purpose — the latter is still exposed, but as a metric only.
_Avoid_: assuming "WritePool"/"WriteQueue" (doc-level names used elsewhere for StorageEngine's own internal mechanism) refer to this type — they don't; Pool is the newer, separate concept.

**Уровни Backpressure (Backpressure levels)**:
NORMAL/WARNING/BACKPRESSURE — the state of a Storage's pending-write queue, exposed for a Consumer to react to. Enforcement is asymmetric: WARNING is observation-only, no Consumer behavior changes on it; only BACKPRESSURE causes farcd's ingest to stop letting new frames in at all (dropped before ever reaching a buffer) — a Consumer-side decision, not something the library itself does. The library's own reaction to BACKPRESSURE is narrower: it withdraws reads' priority share so writes get it back (ADR-011).
_Avoid_: assuming the library itself drops or rejects writes at any level — per its own no-drop guarantee, only a Consumer (like ingest) ever decides to drop incoming data.

**Protected**:
A per-fblock, independent modifier (orthogonal to its `ready`/`bad` state) that exempts it from ever being selected as the next block to overwrite in the writer's cyclic sweep. The library enforces no cap on how many fblocks may be protected at once — controlling a sane share is entirely the Consumer's responsibility. Protecting too many (or all) Ready fblocks can genuinely exhaust a Storage's writable space.

**TOC (Table of Content)**:
One fcontainer's own index — opaque to Storage, interpreted only by the fcontainer's own consumer.

### Channels & ingest

**Политика захвата (CapturePolicy)**:
A per-channel frame filter: decides which incoming frames, and when, are let through into the open fcontainer. Two variants: **continuous** (lets frames through except during an explicit stop) and **event** (lets frames through only around triggers, within prerecord/postrecord windows).

**Recording**:
The live flag saying whether a channel's incoming frames are currently being let through into its fcontainer right now. Distinct from CapturePolicy — CapturePolicy is the configured strategy; Recording is its current on/off output.

**Номер канала (Channel number)**:
An integer `1..65535` that the Consumer uses to tag one data source within an fcontainer. `0` is reserved and never a real channel number.

**Компактная позиция (Channel registry position)**:
An index (`0..C-1`) internal to one Storage that a channel number resolves to for `channel_bitmap`. Storage-local — never copy a position between Storages, only the channel number itself. Only freed for reuse once its reference count (the number of fblocks in this Storage whose `channel_bitmap` still has that position's bit set) reaches zero — that happens purely through the normal physical overwrite cycle, never just because the channel was removed from config.
_Avoid_: confusing this with the channel number itself — the same position index can mean a different channel at different points in time; always read it through the `channel_registry` snapshot embedded in the specific fblock you're looking at, never the Storage's current live one.

### hls_server / playback

**Сегмент (Segment)**:
One HLS playlist unit — a CMAF media segment (`.m4s`), served in a single HTTP response.

**Init-сегмент (Init segment)**:
A CMAF resource carrying codec parameters (`init.mp4`), shared by every media segment of one fcontainer.

**Сетка сегментов (Segment grid)**:
The sequence of time boundaries within one fcontainer that segments are cut along. Never crosses an fcontainer boundary (ADR-019) — a segment always belongs to exactly one fcontainer.
