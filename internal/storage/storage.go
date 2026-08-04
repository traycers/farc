// Package storage implements StorageUnit (docs/docs/archive/02-storage.md
// §4.2): Initializer, the three-path Startup, ConsistencyCheck, Recorder,
// Reader, NotificationBus, HealthMonitor, and the optional SSD Catalog
// mirror (ADR-007) for one Storage. It is the only package that assembles
// and interprets real fblock bytes end to end — everything below it
// (fblock, index, fcontainer, toc, mediatree, ioengine, storageengine) is a
// pure format/algorithm library or a single-purpose engine with no
// knowledge of a specific Storage's actual disk content.
//
// # Gap: every fblock write occupies exactly fblock_size bytes
//
// 03-storage-format.md §10's own arithmetic ("Размер контента: fblock_size
// - 112 - params_size - catalog_size - toc_size") only makes sense if
// content is zero-padded to fill that exact size whenever the real,
// tree-encoded fcontainer is smaller — otherwise TOC/epilog, which are
// located by reading backward from the fixed slot end (index×fblock_size +
// fblock_size, required for offset(index) addressing to work at all, ADR-
// 001), would never land where that arithmetic says they do. This package's
// assembleFblock helper (assemble.go) therefore always produces a buffer of
// exactly fblock_size bytes, left-padding the content section with zeros as
// needed. §3.1 step 4's "Размер TOC и контента — нулевой" for the Storage-
// init write of fblock 0 is read as "zero real/logical bytes" (an absent
// tree, no toc.Build call at all — content is 100% padding, toc_size is a
// true 0), not as "physical footprint is 0 bytes" — the two docs are only
// consistent under this reading.
//
// # Gap: recovering a fcontainer's identity after a crash
//
// 04-storage-operations.md §7.2 builds the catalog snapshot embedded in the
// fblock being written from IndexManager's current in-memory state, with
// only two exceptions called out for the fblock's own entry: its state is
// forced to in_progress, and its channel_bitmap already carries this
// write's channel registrations. IndexManager itself, however, only learns
// the new fcontainer's UUID/begin/end at CompleteWrite — after the write
// already succeeded — so the snapshot physically embedded in the fblock
// being written still shows the previous occupant's (or zeroed) UUID/
// begin/end for its own entry. If the process crashes after the epilogue
// was durably written but before the next fblock's write starts,
// ConsistencyCheck would have no way to recover that fcontainer's identity:
// ADR-001 requires every fblock to be self-contained, but UUID/begin/end
// only ever live in the catalog (03-storage-format.md §6.2) — never in
// Content/TOC.
//
// Resolution: Recorder takes a index.Manager.Snapshot() deep copy after
// BeginWrite and patches its own entry's UUID/Begin/End (and channel_bitmap
// bits) with the new fcontainer's real values before encoding the header —
// generalizing the one exception the docs already grant to channel_bitmap[N]
// to the two other fields that need the same self-description property.
// This keeps every successfully-epilogued fblock genuinely self-contained
// without changing IndexManager's already-tested in-memory semantics
// (BeginWrite/CompleteWrite are unchanged; only what Recorder does with a
// Snapshot() copy changes).
//
// # Gap: the SSD catalog must mirror in-flight writes too, not just completed ones
//
// ADR-007's own lifecycle only calls for updating the SSD catalog "после
// каждой успешной записи" (after each successful write). If that were the
// only update point, a crash between the main disk's epilogue succeeding
// and Recorder's completion step would leave the SSD mirror showing the
// state from BEFORE this write even started — it would never have recorded
// the target index as in_progress at all, since that update only happens
// on success. ConsistencyCheck (run "after loading indices via any path")
// would then find nothing in_progress via the SSD path and silently miss a
// fully-written, epilogue-valid fblock — a real data-loss bug, since the
// main disk's own self-referential in_progress marker (ADR-002) is exactly
// what path 2's header scan relies on to catch this same case correctly.
//
// Resolution: Recorder saves the SSD catalog twice per fcontainer — once
// right after BeginWrite (index marked in_progress, with the patched
// UUID/begin/end/channel_bitmap already known at that point) and once
// after CompleteWrite (index marked ready). This mirrors the main disk's
// two-phase visibility onto the SSD mirror, so ConsistencyCheck's uniform,
// path-agnostic treatment is actually safe for the SSD-catalog path too.
//
// # Deferred: ADR-017 incremental flush
//
// This first implementation of Recorder writes each fcontainer as a single
// whole-buffer write-verify job once the caller's Filler is done, matching
// the pre-ADR-017 model. ADR-017's periodic partial-fchunk flush while a
// fcontainer is still being filled (bounding memory and the crash-loss
// window) is accepted, in-scope design, but not yet implemented — a v1
// simplification analogous to the JobRunner/GeometryManager deferral
// already recorded in PLAN.md.
package storage
