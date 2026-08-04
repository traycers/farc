# farc v1 implementation plan

## Progress

- [x] Phase 1 — `farc/fblock` (32 tests, verified against §5/§6/§7/§9 worked examples)
- [x] Phase 2 — `farc/mediatree` format half (role/type enums, node codec, validator; verified against the 13-node example in `08-array-trees.md` §2)
- [x] Phase 3 — `farc/toc` (`Build`/`Decode`/query primitives; verified byte-for-byte against `08-array-trees.md` §8.3-8.4's own worked output)
- [x] Phase 4 — `internal/fcontainer` (Filler + concurrent allocator; single Filler-wide mutex, verified under `-race`)
- [x] Phase 5 — `internal/index` (IndexManager; found and fixed a real batch-allocation race in the channel-registry reuse rule, see below)
- [x] Phase 6 — `internal/ioengine` (IoBackend: `direct`/`standard`; added `golang.org/x/sys` dependency; O_DIRECT actually works on this sandbox's tmpfs, no skip needed)
- [x] Phase 7 — `internal/storageengine` (write-verify + read/write arbitration; found and fixed a quota-leak race, see below)
- [ ] Phase 8 — `internal/storage` (StorageUnit: Initializer, Startup, ConsistencyCheck, Recorder, Reader)
- [ ] Phase 9 — `internal/ingest` (IngestManager, ChannelIngest, CapturePolicy + gortsplib deps)
- [ ] Phase 10 — `internal/api` (HttpApiServer, EventPushServer, MetricsEndpoint)
- [ ] Phase 11 — `internal/farcd` wiring + `cmd/arch` rewire + `internal/config`

**Deviation from the original sketch below (Phase 3):** to avoid an import cycle, `farc/toc` depends on `farc/mediatree` (not the reverse as first sketched) — it uses `mediatree.NodeType`/`Role`/`Element` directly. The "mediatree query helpers built on toc" (e.g. `FindFrameTimesNear`) don't live inside package `mediatree` itself; they'll be added one layer up (Phase 8's Reader code, which already depends on both packages).

**Stopping point (2026-08-04):** Phases 1–7 done, `go build ./... && go vet ./... && go test ./... -race` all green. Next: Phase 8, `internal/storage` (StorageUnit) — see its sub-order in the build list below.

**Phase 7 design note.** `storageengine.Engine` exposes a deterministic `Step()` (one fchunk write-verify or one read portion per call, chosen per ADR-005/ADR-011) instead of an opaque background loop, specifically so tests can drive it synchronously and assert exact M/K interleaving order without timing dependence; `Run(ctx)` is a thin convenience wrapper looping `Step()` on its own goroutine for production use, added even though no caller wires it up yet (Phase 8's Recorder will be the first). `EnqueueWrite`/`EnqueueRead` return tickets (`Wait()` blocks on a channel) rather than blocking the caller directly, so producer goroutines (Recorder, multiple Readers) and the single `Step()`-driving goroutine stay decoupled. Retry-on-a-new-index after a corrupted write-verify is explicitly **not** this package's job (`WriteResult.Corrupted` + `FailedOffset` is all it reports) — picking a new physical index belongs to Recorder/`IndexManager` in Phase 8, matching the dependency direction (`storageengine` depends only on `ioengine`, not `index`).

**Bug found during verification:** the M/K quota (ADR-011) must go fully dormant under BACKPRESSURE, not just get zeroed at the moment a `Step()` call observes it — an earlier version reset `quotaRemaining` to 0 only at the top of `Step()`, but `stepWriteLocked` unconditionally re-granted a fresh quota window after every `QuotaEvery`-th chunk regardless of level, so a grant earned while still under backpressure survived into the very next `Step()` call and fired a read one step early the instant the queue dropped back below the backpressure threshold. Fixed by also gating the grant logic itself on `level != LevelBackpressure` (both the reset and the accrual live under the same condition now) — caught by `TestQuota_DisabledUnderBackpressure`, which drains a saturated write queue down through the backpressure threshold and asserts every single step in that transition is a write.

## Context

`farc`'s design docs (`docs/docs/archive/*.md`, 17 ADRs) are complete and detailed, but the code is still just a `cmd/arch` cobra stub — `internal/` is empty (a prior prototype package, `traa`, was deleted). Over a long design-review session we closed every open scoping question blocking a v1 plan: removed a stray DDD aside, fixed a real drift between `01-architecture.md` and `11-service-composition.md` (Initializer now goes through Archive, not a standalone CLI), fixed a naming drift on `write_mode` across three docs, accepted ADR-011 as-is and ADR-012 in a simplified v1-only form (plain `now()`, no anomaly protection), decided v1 scope (RTSP ingest, SSD Catalog, and EventPushServer are IN; `GeometryManager`/`Importer` — and therefore any `JobRunner` orchestrator component — are OUT), and finalized the external JSON config schema (`04-storage-operations.md` §2.1). `TODO.md` tracks all of this as closed.

This plan turns that settled design into a concrete, phased Go build order. While exploring, three Explore agents extracted full byte-level format/algorithm detail from the remaining unread docs (`05-data-format.md`, `06-toc-format.md`, `07-media-tree.md`, `08-array-trees.md`, `09-fcontainer-write-path.md`, `00-requirements.md`, and 13 ADRs), and a repo scan surfaced an important asset: a gitignored `temp/mediamtx-1.19.3` reference copy of the mediamtx RTSP server, which depends on `github.com/bluenviron/gortsplib/v5` and `github.com/bluenviron/mediacommon/v2` — mature, MIT-licensed, actively-maintained libraries that do exactly the RTSP/RTP/SDP/codec-parameter work `ChannelIngest` needs. **Recommendation: use them as real dependencies instead of writing an RTSP/RTP client from scratch.** One caveat found during verification: mediamtx's own `go.mod` requires `go 1.26.0`; check whether `gortsplib/v5` itself forces that floor before bumping farc's `go 1.25.3` directive (Phase 9 action item, not a blocker).

A Plan sub-agent (fed the full distilled spec) proposed the package layout and build order below; I verified its two load-bearing claims directly against the docs and the mediamtx source before accepting them: the exact catalog-entry formula (`33 + ceil(C/8)` bytes/entry — confirmed against `03-storage-format.md` §6.2, byte-for-byte) and the `gortsplib.Client` usage shape (confirmed against `temp/mediamtx-1.19.3/internal/staticsources/rtsp/source.go`).

## Package layout

Two kinds of packages: **root-level** (`farc/...`) for pure format/algorithm libraries an external process (Video Gateway, a future recovery CLI per `01-architecture.md` §4) might need to import verbatim; **`internal/...`** for everything farcd-process-specific.

| Package | Depends on | Responsibility |
|---|---|---|
| `farc/fblock` | stdlib | fblock binary format: 56-byte fixed prologue, JSON params, catalog SoA (channel registry + per-fblock `flags/uuid/begin/end/channel_bitmap` arrays, exact formula from `03-storage-format.md` §6.2), 3×CRC32, epilogue, geometry math (`min_container_share` check, ADR-013). Pure `[]byte` encode/decode, no I/O. |
| `farc/mediatree` | stdlib | `role`/`type` const blocks (`07-media-tree.md` §3.1, append-only, never renumber), Content-node (AoS) encode/decode, sequential-scan validator (port of `08-array-trees.md` §13 checklist — doubles as corruption detection). |
| `farc/toc` | `farc/mediatree` | TOC binary format (64-byte-aligned SoA columns, `06-toc-format.md` §3-4), `Build()` (the DFS reorder algorithm — depth, subtree-size, DFS `pos` via prefix sum, permute+translate `parent`/`sibling`, §5), `Decode()`, and role-agnostic query primitives (`SubtreeRange`, `IsAncestor`, `ScanByRole`, `TimeRange`, `CoveringSubtreeRoot`/LCA-of-set) shared by both the ADR-004 client path and the ADR-016 farcd-side fallback. |
| `internal/fcontainer` | `farc/mediatree` | The **Filler**: `AddStreamParams`/`AddFrames` (`09-fcontainer-write-path.md`'s entire public contract) plus the concurrent id/offset/`lastchild` allocator (gap resolution, see below). |
| `internal/index` | `farc/fblock` | **IndexManager**: in-memory catalog + cursor, `select_next_index` (`04-storage-operations.md` §6.2, cyclic/fill_until_full), channel registration/reuse, UUID/candidate lookup, protected/retention mutation. |
| `internal/ioengine` | `golang.org/x/sys/unix` (linux) | `IoBackend` interface; `direct` (O_DIRECT, alignment probe, outward-rounding reads) and `standard` (portable, degraded verify guarantee) backends, chosen by GOOS or config override (ADR-010). |
| `internal/storageengine` | `internal/ioengine` | **StorageEngine**: fchunk write-verify loop, read/write arbitration (ADR-005 priority, ADR-011 M=16/K=4 quota, disabled under BACKPRESSURE). No fblock-structure knowledge — offsets/lengths only. |
| `internal/storage` | `farc/fblock`, `farc/toc`, `farc/mediatree`, `internal/fcontainer`, `internal/index`, `internal/storageengine` | **StorageUnit**: Recorder, Reader, NotificationBus, HealthMonitor, SSD Catalog — plus Initializer, the 3-path Startup, and ConsistencyCheck **inlined here as methods, no `JobRunner` package** (matches the v1 scope decision). |
| `internal/ingest` | `gortsplib/v5`, `mediacommon/v2`, `internal/fcontainer`, `farc/mediatree` | IngestManager, ChannelIngest, CapturePolicy (`continuous`, `event`; `schedule` rejected with a clear error, not silently stubbed — see below). |
| `internal/api` | `internal/storage`, `internal/ingest` | StorageRegistry, HttpApiServer, EventPushServer (WS), MetricsEndpoint — minimal new route set (below); no spec exists for this anywhere in the docs. |
| `internal/config` | stdlib | JSON config schema exactly per `04-storage-operations.md` §2.1 (`time.ParseDuration` for capture-policy fields); replaces the current empty `cmd/arch/config` stub. |
| `internal/farcd` | everything above | Process wiring: load config → open/init each Storage → build IngestManager per channel → wire the backpressure status link (below) → start the three servers → graceful shutdown. |

Dependency direction is a strict DAG; `internal/storage` is the only thing that touches disk; `internal/ingest` never touches `internal/storage` directly except through the Filler and the backpressure flag.

## Build order and verification

1. **`farc/fblock`.** Verify: table-driven round-trip tests against the doc's own byte-offset tables (§5/§6/§7/§9); the `C=256` → 65-byte/entry worked example; CRC-corruption cases mapped to the §7.1 diagnosis table; `min_container_share` rejection boundary.
2. **`farc/mediatree` format half** (enum + node encode/decode + validator). Verify: hand-encode the 13-node tree from `08-array-trees.md` §2, decode, `Validate()` passes; flip one byte to break `parent[i]<=i`, assert it's caught.
3. **`farc/toc`.** Verify: golden test — feed Phase 2's tree into `Build`, assert `new2old`/`old2new`/`parent'`/`sibling'` match the docs' own worked tables (`08-array-trees.md` §8.3, `06-toc-format.md` §8) exactly; column padding offsets match the stated formula; `SubtreeRange`/`IsAncestor`/LCA-of-set checked against the docs' own worked answers.
4. **`internal/fcontainer` (Filler)** — implements the concurrency gap resolution below. Verify: N-goroutine torture test calling `AddStreamParams`/`AddFrames` concurrently across many (channel,stream) pairs, then run Phase 2's validator over the frozen result, under `-race`; a second test exercises the literal `09-fcontainer-write-path.md` contract (returned `configID` reuse, frame order preserved).
5. **`internal/index`.** Verify: table-driven tests transcribing the §6.2 pseudocode cases (wraparound, cyclic vs fill_until_full, `NO_SPACE`) and the §7.1.1 channel-registry reuse/exhaustion cases.
6. **`internal/ioengine`.** Verify: `standard` round-trips on a plain tmp file; `direct` gated by `//go:build linux` with a runtime skip if `O_DIRECT` open fails (e.g. tmpfs); alignment math unit-tested independent of the real open call.
7. **`internal/storageengine`.** Verify: fake `IoBackend` told to corrupt fchunk N → assert bad+retry-on-next-index; simulated saturated write queue → assert M=16/K=4 accounting exactly, and quota zeroed under BACKPRESSURE.
8. **`internal/storage`** (the big integration phase) — build sub-order: Initializer → Startup path 1 (SSD catalog) + path 2 fallback scan + catalog rebuild → ConsistencyCheck → Recorder (buffer pool, FIFO, ADR-017 periodic flush via `Filler.Len()` polling, catalog-snapshot assembly, write-verify dispatch) → Reader (UUID resolve, TOC read, ranged read, channel+time candidates) → NotificationBus → HealthMonitor → SSD Catalog mirror.
   Verify: init an 8×1MiB-fblock Storage on a tmpfs file, write several overlapping-channel fcontainers, kill and reopen via both the SSD-catalog path and (catalog deleted) the fallback-scan path, assert identical `IndexManager` state; fault-injection test truncating a fblock before its epilogue (simulated power loss) → `ConsistencyCheck` marks it `bad`; a properly-finished one → `ready`; mid-fchunk write failure → bad+retry end to end.
9. **`internal/ingest`.** Verify: `CapturePolicy` state-table tests (`from_time` replay, `stop_at=max(...)` extension, idle-on-timeout) against a fake frame source and fake Filler — no RTSP needed; define a small interface around the `gortsplib.Client` subset `ChannelIngest` actually calls so most tests inject a fake; one slower test runs a real in-process `gortsplib` RTSP *server* over loopback serving synthetic H.264, asserting the decoded Content tree has the expected SPS/PPS/frame/GOP shape. **Action item:** confirm `gortsplib/v5`'s own minimum Go version before/while adding it as a dependency (mediamtx itself needs 1.26, farc is on 1.25.3).
10. **`internal/api`.** Verify: `httptest` per route against a fake `StorageRegistry`; WS subscribe→push test against a fake `NotificationBus`.
11. **`internal/farcd` + `cmd/arch` rewire.** Verify: opt-in (`//go:build integration`) end-to-end smoke test — real `farcd.Run` against a tmpfs storage + loopback synthetic RTSP source, then HTTP read-back and `/metrics` scrape.

## Gap resolutions (design work the docs explicitly left open)

- **Channel-registry reuse race within one buffer (found while implementing Phase 5, not in the original plan).** `04-storage-operations.md` §7.1.1's reuse rule ("reuse a position whose reference count is 0") reads bit references from the *committed* catalog — but a position registered for a brand-new channel earlier in the very same buffer's channel list has no committed bit yet either (the bit is set later, in §7.2 step 3), so a naive per-channel implementation of the rule would let a later channel in the same list immediately steal an earlier one's just-issued position. Fixed by resolving a buffer's whole channel list in one call (`Manager.RegisterChannels`), excluding this-batch allocations from the reuse search; cross-buffer calls remain safe as-is because Recorder is single-writer and commits each write's bits before the next buffer is processed.

- **Filler concurrency** (the load-bearing gap — `05-data-format.md` §6 and `09-fcontainer-write-path.md` §5 both explicitly disclaim specifying this). Since Content `id` is pure sequence position with no gaps, reserving a node's id, its exclusive byte range in the buffer, and updating `lastchild[parent]` (for `sibling`) must happen as **one atomic transaction** — a single `sync.Mutex`-guarded `reserve(parent, valueLen) (id, sibling, offset)` critical section; the actual header+value byte copy happens lock-free afterward into the caller's exclusively-owned range. Rejected alternatives: per-parent atomics alone (doesn't solve the joint id/offset/lastchild coupling) and a single-writer funnel goroutine (adds latency for no benefit — the critical section is already O(1)).
- **ADR-017 `ready_bytes` plumbing**: Filler exposes a cheap `Len()`; Recorder polls it on a ticker (period ≤ `T`), computes `writable = floor(len/alignment)*alignment` minus already-flushed, pushes the delta to StorageEngine. Filler never initiates anything, matching "push-notified by Recorder, Filler uninvolved" literally.
- **`CapturePolicy.schedule`**: deferred explicitly, not silently stubbed. `10-capture-policy.md` §5.3 itself calls it "a version after event," and the finalized config schema's worked examples only cover `continuous`/`event`. `internal/config` rejects `capture_policy.type == "schedule"` with a clear "not implemented in v1" error at load time.
- **`StorageUnit → CapturePolicy` backpressure signal** (open question in `10-capture-policy.md` §8): extend the existing get-buffer/return-buffer exchange rather than invent a new component — Recorder owns a `map[channelID]*atomic.Bool` ("skip frames"), updated on NORMAL/WARNING/BACKPRESSURE transitions; `internal/farcd` wiring hands each `ChannelIngest` a read-only reference to its own channel's flag, checked once per frame.

## Minimal HTTP/WS/metrics API (new design — no spec exists in the docs for this at all)

- `POST /storages` (runs Initializer inline, registers in StorageRegistry) · `GET /storages`
- `GET /storages/{id}/fcontainers/{uuid}/toc` · `GET /storages/{id}/fcontainers/{uuid}?ranges=off:len,...` · `GET /storages/{id}/fcontainers/{uuid}` (whole export, ADR-003)
- `GET /storages/{id}/candidates?channel=&t1=&t2=` (ADR-014) · `GET /storages/{id}/resolve?channel=&t1=&t2=` (ADR-016 fallback, via the shared `farc/toc` query layer)
- `POST .../protected {value}` · `PATCH /storages/{id} {retention_days}`
- `POST /channels/{id}/capture-policy {type,params}` (`schedule` → `501`) · `POST /channels/{id}/events {t}` (plain one-shot POST, sidesteps the long-idle-timeout concern from `10-capture-policy.md` §8 since v1 isn't a long-poll shape)
- `GET /metrics` (Prometheus exposition, `02-storage.md` §8's metric table)
- WS: one subscribe message on connect (`{storage, want, channels}`); server pushes typed JSON frames for compact events and per-channel TOC. No reconnect catch-up for compact events in v1 (consciously deferred); TOC catch-up uses `/resolve`.

## Critical files

- `docs/docs/archive/03-storage-format.md` — exact byte layout `farc/fblock` must match.
- `docs/docs/archive/06-toc-format.md`, `docs/docs/archive/08-array-trees.md` — TOC build algorithm + worked golden-test numbers for `farc/toc`.
- `docs/docs/archive/09-fcontainer-write-path.md`, `docs/docs/archive/05-data-format.md` — the Filler's public contract and the concurrency gap.
- `docs/docs/archive/04-storage-operations.md` — Initializer/Startup/ConsistencyCheck/`select_next_index`, all inlined into `internal/storage`.
- `temp/mediamtx-1.19.3/internal/staticsources/rtsp/source.go`, `temp/mediamtx-1.19.3/internal/protocols/rtsp/to_stream.go` — reference shape for `internal/ingest`'s `gortsplib` usage.
- `cmd/arch/commands/default.go`, `go.mod` — current entrypoint and dependency set to extend in Phases 9/11.
