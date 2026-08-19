# Persistent on-disk TOC cache + catalog-diff bootstrap for hls_server

Status: resolved

## Problem

`internal/tocindex.EventSubscriber.bootstrap` (first connect, and again
after *every* WS disconnect) currently does, per configured channel:

    Candidates(channel, 0, math.MaxUint64)   -- full retention window
    GetTOC(uuid) for every candidate          -- one HTTP round trip each

`ChannelIndex` itself is pure in-memory (`internal/tocindex/index.go`) and
is thrown away and rebuilt from scratch on every `hls_server` process
restart, and re-bootstrapped (not just gap-filled) on every WS reconnect
per `EventSubscriber.Run`'s retry loop.

`Candidates` is cheap on farcd's side (`internal/index.Manager.Candidates`
reads the in-memory catalog, no disk I/O). The expensive part is
`GetTOC`: each call reads a fcontainer's TOC section off disk through the
arbitrated read path (`Unit.readRange` → `StorageEngine.EnqueueRead`),
and per ADR-005 reads always yield to writes. On a large disk (the
18 TB/full-day-init case ADR-006 already worries about) with a full
retention window's worth of fblocks and an actively writing channel, doing
this serially (or even moderately parallel) for every fblock in
retention, on every restart/reconnect, can take a very long time — exactly
the concern the user raised (2026-08-13): *"farcd не сможет это сделать,
т.к. при большом размере хранилища на hdd и при активной записи - это
будет выполняться очень долго"*.

## Recommendation (agreed with the user, 2026-08-13)

1. **Persistent on-disk TOC cache in `hls_server`**, keyed by
   `(storage id, fcontainer UUID)`, storing exactly the bytes `GetTOC`
   already returns (`toc.Decode`-able as-is). Survives process restarts —
   the whole point is to not start from zero every time. Lives under a
   dedicated subpath of `HLS_SERVER_CACHE_DIR` (decided with the user,
   2026-08-13), sibling to `internal/segmentcache`'s disk cache rather
   than a separate config var or volume — e.g.
   `$HLS_SERVER_CACHE_DIR/toc/<storage id>/<uuid>`, alongside
   `internal/segmentcache/disk.go`'s existing segment layout under the
   same root.
2. **A cheap way to learn which UUIDs are still live**, without walking
   `Candidates`+`GetTOC` over the full window. farcd already holds this
   for free: `internal/index.Manager.Snapshot()` returns a full
   `*fblock.Catalog` (SoA: State/UUID/Begin/End/Protected per fblock,
   already the source `handleGetFblock` reads from for a single index) —
   an in-memory clone, no disk I/O, analogous to what ADR-007/008's SSD
   catalog gives farcd itself at its own startup to skip a full header
   scan. There is currently **no HTTP endpoint that returns this in bulk**
   — only per-index (`GET /storages/{id}/fblocks/{index}`) and
   range-filtered (`GET /storages/{id}/candidates`, which still requires a
   channel+range and doesn't expose UUIDs beyond len(indices)). This needs
   a new endpoint, e.g. `GET /storages/{id}/catalog`, returning
   `{index, state, uuid, begin, end}` for every fblock (or at least every
   `Ready` one) — cheap, in-memory, no read-path contention with writes.
3. **Bootstrap becomes a diff, not a refetch**: on connect/reconnect,
   `hls_server` calls the new bulk endpoint once per storage, then:
   - UUIDs present in the catalog but missing from the local disk cache →
     `GetTOC` (only the delta — small in the steady state, since most
     retained fblocks survive between restarts).
   - UUIDs present in the local disk cache but no longer in the catalog
     (aged out / overwritten by the cyclic writer) → evict from cache.
   - Everything else is already on disk locally → load straight into
     `ChannelIndex`, no farcd round trip at all.

## Decided (2026-08-13)

- **Cache location**: dedicated subpath under `HLS_SERVER_CACHE_DIR`, not
  a separate config var/volume (see point 1 above).
- **Eviction policy**: none of its own. Catalog-diff eviction (an entry is
  dropped exactly when its UUID drops out of farcd's catalog) is
  sufficient — no size quota, no LRU. This relies on TOC entries being
  metadata-only and therefore small relative to
  `internal/segmentcache`'s media segments; revisit only if that
  assumption stops holding (e.g. very high channel/fblock-count storages).

## Decided (2026-08-13, continued)

- **Bulk catalog endpoint filtering**: unfiltered — `GET /storages/{id}/catalog`
  returns every fblock's `{index, state, uuid, begin, end}`, no channel
  bitmap, no channel query param. The cache/diff logic operates at
  `(storage, uuid)` grain regardless, so channel filtering is a
  payload-size optimization with no confirmed need; revisit only if it
  matters in practice.
- **Push-path cache writes**: yes, both paths. `EventSubscriber` writes into
  `toccache` whether a TOC came from a pushed WS frame (issue 01) or a
  `GetTOC` fallback/bootstrap fetch — the cache mirrors whatever
  `ChannelIndex` currently holds.

## Implementation (2026-08-13)

Done, TDD (`/tdd`), red→green per seam:

1. **`internal/toccache`** (new package): a plain on-disk `Cache` --
   `New(dir)`, `Put`/`Get`/`Delete`/`List(storageID)` -- keyed by
   `(storageID, uuid)`, one file per entry at `dir/storageID/uuidHex`. No
   eviction policy of its own (decided 2026-08-13, original section above).
   Tests: `TestCache_PutThenGet`, `TestCache_DeleteAndList`.
2. **`internal/api`**: new `GET /storages/{id}/catalog` (`catalog.go`),
   returning `catalogEntry{index, state, uuid, begin, end}` for every
   fblock via `unit.Index().Snapshot()` -- `uuid`/`begin`/`end` only
   populated for `state == "ready"`, mirroring `fblockInfo`'s own
   conventions. Tests: `TestHandleGetCatalog`,
   `TestHandleGetCatalog_UnknownStorage` (`catalog_test.go`).
3. **`internal/hlsclient`**: new `Client.Catalog(ctx, storageID) ([]CatalogEntry, error)`
   decoding the above. Test: `TestClient_Catalog`.
4. **`internal/tocindex/subscriber.go`**: `bootstrap` no longer calls
   `Candidates`+`GetTOC` per channel over the full retention window.
   Instead: one `Client.Catalog` call, build the live `(uuid -> ready)` set
   (explicitly excluding a Ready-but-uuid-zero entry -- see the pitfall
   below), evict any `toccache` entry no longer live, then for each live
   uuid load from `toccache` if present (falling back to `GetTOC` on a
   decode failure) or `GetTOC` once and cache the result
   (`indexAndCache`). `handleWriteCompleted`'s existing pushed-TOC path
   (issue 01) also now writes the raw pushed bytes into `toccache`.
   `NewEventSubscriber` gained a required `*toccache.Cache` parameter.
   Tests: `TestEventSubscriber_BootstrapUsesCacheDiff` (cached + still-live
   uuid costs zero `GetTOC` calls), `TestEventSubscriber_BootstrapEvictsStaleCacheEntry`
   (a cached uuid no longer in the catalog gets deleted from disk).
5. **`internal/hlsconfig`**: `cache_dir` (`HLS_SERVER_CACHE_DIR`) is now
   required unconditionally, not only when `cache_backend == "disk"` --
   `toccache` always needs a local directory regardless of which backend
   the *segment* cache uses, so an `s3`-backend deployment no longer gets a
   free pass on it. This is a small scope extension beyond the two
   decisions recorded above them, made without a separate check-in since it
   follows directly from "reuse `HLS_SERVER_CACHE_DIR`, no new config var"
   once the s3-backend gap surfaced during implementation. Test:
   `TestLoad_MissingCacheDirEnvRejected_S3Backend`.
6. **`internal/hlsd`**: `New` builds one `toccache.Cache` rooted at
   `cfg.CacheDir/toc` (`tocCacheSubdir`) and passes it to every
   per-channel `NewEventSubscriber` call (`startChannel`) -- shared across
   every channel on the same storage, since the cache is keyed by
   `(storageID, uuid)`, not by channel.

**Pitfall found and fixed during implementation**: fblock 0 is marked
`Ready` in the runtime catalog the instant `Init` succeeds
(`internal/storage/init.go`, matching `02-storage.md` §5's "IndexManager
will treat it as Ready as soon as this call returns successfully"), before
any fcontainer is ever written into it -- its `uuid`/`begin`/`end` are all
zero. `Candidates` never surfaces this (its channel-bitmap filter excludes
it implicitly, since nothing was ever written under any channel), but the
new unfiltered bulk catalog does return it as `state: "ready"`. `bootstrap`
now explicitly excludes a `state == "ready"` entry whose uuid is the zero
value from its live set -- without this, every bootstrap/reconnect
uselessly tried (and failed) a `GetTOC` for fblock 0's phantom uuid on any
storage using the small-`N` fixture geometry (and, by the same logic, on
any real storage's very first fblock immediately after `Init`).

`go build ./...`, `go vet ./...`, full `go test ./...`, and
`golangci-lint run ./...` all clean after this change.
