# hls_server implementation plan

## Progress

- [x] Phase 1 — `internal/hlsclient` (typed HTTP+WS client for farcd's read API)
- [x] Phase 2 — `internal/tocindex` (ChannelIndex + EventSubscriber, ADR-018)
- [x] Phase 3 — `internal/playlist` (PlaylistBuilder, ADR-019 segment grid)
- [x] Phase 4 — `internal/segment` (SegmentBuilder, CMAF mux via mediacommon/v2)
- [x] Phase 5 — `internal/segmentcache` (disk cache, LRU eviction)
- [x] Phase 6 — `internal/hlsapi` (player-facing HTTP routes)
- [x] Phase 7 — `internal/hlsconfig` + `internal/hlsd` + `cmd/hls_server` (wiring, CLI)
- [x] Phase 8 — `taskfile.yaml` build task for the new binary

## Context

`docs/docs/archive/12-hls-server.md` (plus `adr/018-hls-index-toc-push.md` and `adr/019-hls-segment-fcontainer-boundary.md`) already designed `hls_server`: a new process that turns farcd's archive into browser-playable HLS (H.264+AAC, VOD-only, no live, no transcoding — pure CMAF remux). This plan is the implementation of that design.

farcd itself (`internal/farcd`, `internal/api`, `internal/storage`, `internal/index`, `toc`, `mediatree`, `fblock`) is a complete, working v1 — not a skeleton, despite `CLAUDE.md`'s stale claim that `internal/` is empty. `hls_server` is a second, independent binary in the same module, talking to farcd only through its already-implemented external API (`internal/api/server.go`'s routes and `internal/api/eventpush.go`'s WebSocket push) — never touching Storage directly.

Two things the design doc got slightly wrong once checked against the real code (see Gap resolutions): `EventPushServer`'s push message does **not** carry TOC bytes inline with `fblock.write.completed` (only `{type,name,index,uuid,severity,reason}` — `internal/api/eventpush.go`), and the root-level `toc`/`mediatree` packages (not `internal/`) turn out to be directly importable by `hls_server`, so the TOC-walking logic ADR-016's fallback already implements (`internal/api/query.go`'s `resolveChannelFrames`) doesn't need to be reinvented — `hls_server` reuses the same `toc.SubtreeRange`/`ScanByRole`/`InRange`/`TimeRange`/`InlineValue`/`ContentOffset` primitives directly.

## Package layout

| Package | Depends on | Responsibility |
|---|---|---|
| `internal/hlsclient` | `toc`, `mediatree`, `gorilla/websocket` | Typed HTTP+WS client for farcd's `HttpApiServer`/`EventPushServer` (candidates, resolve, TOC fetch, ranged read, WS subscribe) |
| `internal/tocindex` | `hlsclient`, `toc`, `mediatree` | `ChannelIndex` (per-channel, time-ordered fcontainer records + decoded TOC) and `EventSubscriber` that keeps it current (ADR-018) |
| `internal/playlist` | `tocindex`, `toc`, `mediatree` | Builds a static VOD `.m3u8` for `(channel, t1, t2)`; segment grid confined to one fcontainer (ADR-019) |
| `internal/segment` | `tocindex`, `hlsclient`, `mediacommon/v2/pkg/formats/fmp4`, `mediacommon/v2/pkg/formats/mp4/codecs` | Muxes a fcontainer's frames into CMAF init/media segments |
| `internal/segmentcache` | (none) | Disk-backed cache of built segments, quota + LRU eviction |
| `internal/hlsapi` | `playlist`, `segment`, `segmentcache` | HTTP routes for the web player (`playlist.m3u8`, `init.mp4`, `seg.m4s`) |
| `internal/hlsconfig` | (none) | JSON config: farcd endpoints, channel→storage mapping, segment duration, cache dir/quota |
| `internal/hlsd` | all of the above | Top-level orchestrator (`New`/`Run`/`Shutdown`), mirrors `internal/farcd.Farcd` |
| `cmd/hls_server` | `hlsd`, `hlsconfig` | cobra CLI entrypoint, mirrors `cmd/farc` |

Dependency direction is a DAG matching the project's existing flat `internal/` layering (no package imports back toward `cmd/`); no new external dependencies are needed — `bluenviron/mediacommon/v2` and `gorilla/websocket` are already in `go.mod` (used today only by `internal/ingest` for RTSP capture).

## Build order and verification

1. **`internal/hlsclient`.** Typed client: `GetTOC`, `ReadRanges`, `Candidates`, `Resolve` (bootstrap-only, ADR-016/018) over HTTP, `Subscribe(ctx, storage, channels) (<-chan Event, error)` over `GET /events/ws`. Its wire structs mirror `internal/api`'s unexported `candidateInfo`/`resolvedFrame`/`subscribeMessage`/`pushMessage` — those can't be imported (unexported, and semantically an HTTP wire contract, not a Go API), so `hlsclient` defines its own matching JSON-tagged types. Verify: `httptest.Server` wrapping a real `api.NewHttpApiServer`+`api.NewEventPushServer` over a `storage.Unit`, built with a small local re-creation of `internal/api/testutil_test.go`'s `newTestUnit`/`writeVideoFrame` pattern (can't import a `_test.go` helper across packages — see Gap resolutions); assert decoded values match byte-for-byte, cross-checked against `spec/tsp-output/openapi.yaml`.

2. **`internal/tocindex`.** `ChannelIndex` (per-channel `[]Record{UUID, StorageID, Begin, End, Columns *toc.Columns}`) + `EventSubscriber`: on `fblock.write.completed`, fetch TOC via `hlsclient.GetTOC` + `toc.Decode` (see Gap resolutions — push does not carry TOC inline) then insert; on `fblock.deleted`, remove; on (re)connect, bootstrap via `hlsclient.Candidates`+`GetTOC` for the channel's full retention window. Verify: table-driven tests against a fake `hlsclient` confirming insert/remove ordering and bootstrap-then-live convergence; one integration test against the phase-1 fixture server, writing several fcontainers and confirming the index matches ground truth via both live push and cold-start bootstrap.

3. **`internal/playlist`.** `Build(idx, channel, t1, t2, targetDur) (string, error)`: per-record segment grid confined to `[Begin, End]` (ADR-019), boundaries snapped to the nearest `frame_kind==I` at/after `Begin + n*targetDur` (reusing `toc.ScanByRole`/`TimeRange` exactly as `resolveChannelFrames` in `internal/api/query.go` does), `#EXT-X-DISCONTINUITY` inserted between adjacent records with differing active SPS/PPS. Verify: golden-file `.m3u8` tests — single record fits window; window spans two records with identical config; spans two records with different config (assert discontinuity tag); trailing record too short for one full segment (assert one short segment, not zero).

4. **`internal/segment`.** `BuildInit`/`BuildMedia` using `fmp4.Init{Tracks: []*fmp4.InitTrack{{Codec: &codecs.H264{SPS, PPS}}, {Codec: &codecs.MPEG4Audio{...}}}}.Marshal` for init segments and `fmp4.Sample{Duration, PTSOffset, IsNonSyncSample, Payload}` + `fmp4.Part`/`PartTrack` for media segments; frame bytes fetched via `hlsclient.ReadRanges` using offsets already known from `rec.Columns` (`toc.ContentOffset`) — no farcd-side resolve call per segment, which is the entire point of ADR-018's push-built index. Verify: build a segment from a fixture fcontainer, mux, then re-parse with `fmp4.Init.Unmarshal` (or a minimal box walk for media) and assert track count/timescale/SPS-PPS/frame count/keyframe flags/timestamps match the source exactly.

5. **`internal/segmentcache`.** `Get(key)`/`Put(key, []byte)`, key = `(storageID, uuid, segIdx)`, quota-bounded LRU eviction. Verify: round-trip write/read; eviction under quota pressure removes least-recently-used first; two different requested playback windows covering the same `(uuid, segIdx)` resolve to the same key (the caching payoff ADR-019 is justified by).

6. **`internal/hlsapi`.** `http.Handler` (stdlib `http.ServeMux`, matching `internal/api/server.go`'s style — no router library) wiring `GET /channels/{ch}/hls/{t1}/{t2}/playlist.m3u8` → `playlist.Build`, `GET /segments/{storage}/{uuid}/{n}/{init.mp4|seg.m4s}` → cache-then-build-then-cache. Verify: end-to-end `httptest` — request a playlist spanning a cache-cold segment, fetch every listed segment URL, validate CMAF structure (reuse phase 4's check), request the same segment again with the underlying farcd fixture made unreachable and confirm it still succeeds from cache.

7. **`internal/hlsconfig` + `internal/hlsd` + `cmd/hls_server`.** Config: strict JSON (`DisallowUnknownFields`, matching `internal/config`'s style) — farcd endpoints list, channel→storage mapping, `target_segment_duration`, `cache_dir`, `cache_quota_bytes`. `hlsd.New`/`Run`/shutdown mirrors `internal/farcd/farcd.go` exactly (open clients → start subscriptions → one `*http.Server` → graceful shutdown on `ctx.Done()`). `cmd/hls_server/{main.go, commands/index.go, commands/default.go}` mirror `cmd/farc`'s cobra wiring (`-c/--config`, `godotenv.Load`, `signal.NotifyContext(SIGINT, SIGTERM)`). Verify: `go build -o hls_server ./cmd/hls_server`; one full-stack test — real farcd fixture (phase 1) + a real `hlsd` against it, actual HTTP GET of a playlist and its segments through the whole binary, decoded frames compared against what was written into the source fcontainer.

8. **Build tooling.** Add a `build/hls_server` task to `taskfile.yaml` (today's `build/app` is hardcoded to `z_name: farc`, one binary only) and point `build` at both. Low priority relative to 1–7, but needed before `task build` actually builds this binary.

## Gap resolutions

- **TOC is not inline with the push event.** The design doc assumed `fblock.write.completed` carries TOC alongside it; the real `pushMessage` (`internal/api/eventpush.go`) only has `{type,name,index,uuid,severity,reason}`. Resolved in phase 2: `EventSubscriber` does a synchronous follow-up `GET .../toc` per event before indexing it. This keeps ADR-018's actual property intact — index updates are driven by farcd's write rate, not by playback traffic — it just costs one extra HTTP round trip per new fcontainer instead of zero. Worth a small correction to `12-hls-server.md`/ADR-018's wording later, as a follow-up doc change outside this plan.
- **Segment grid snap algorithm**, left unspecified in `12-hls-server.md` §6.2: boundary = earliest `frame_kind==I` at or after `record.Begin + n·target_segment_duration`, clipped to `record.End` if the record ends first.
- **Disk cache eviction policy**, left open in ADR-019/`12-hls-server.md` §8: LRU by last access, bounded by `cache_quota_bytes` — matches the user's explicit choice of disk-only caching.
- **Unsupported codecs (H.265, PCM/G711).** Out of scope (H.264+AAC only, per the earlier design discussion). `tocindex` indexes a stream only when its `RoleCodecVideo`/`RoleCodecAudio` value is `mediatree.CodecH264`/`mediatree.CodecAAC`; an unsupported track on an otherwise-supported channel is silently omitted from output (e.g. video-only HLS if audio is G.711), logged once per record, not a hard failure.
- **`канал → хранилище` duplication** between farcd's config and `hlsconfig`. No automatic sync for v1 (matches the project's existing pattern of deferring cross-process coordination) — `hlsconfig.Load` validates every configured channel's storage id resolves to a configured farcd endpoint, failing fast at startup on a mismatch rather than at request time.
- **Test fixture duplication.** `internal/api/testutil_test.go`'s `newTestUnit`/`writeVideoFrame` are unexported `_test.go` helpers and can't be imported from `hls_server`'s own tests; this plan recreates the ~30-line equivalent locally (phase 1) rather than promoting them into a new shared package for two call sites.

## Critical files

- `docs/docs/archive/12-hls-server.md`, `adr/018-hls-index-toc-push.md`, `adr/019-hls-segment-fcontainer-boundary.md` — the design this plan implements.
- `internal/api/eventpush.go` — exact WS wire format `hlsclient.Subscribe` must match, and the TOC-inline gap.
- `internal/api/query.go` — exact `candidates`/`resolve` JSON shapes and the `resolveChannelFrames` traversal `playlist`/`segment` mirror.
- `internal/api/fcontainers.go` — exact TOC/ranges HTTP routes and shapes.
- `toc/query.go`, `mediatree/role.go` — reused directly (root-level, importable): `SubtreeRange`/`ScanByRole`/`InRange`/`TimeRange`/`InlineValue`/`ContentOffset`, `Role`/`Codec`/`FrameKind` constants.
- `internal/config/config.go`, `internal/farcd/farcd.go` — style template for `hlsconfig`/`hlsd`.
- `cmd/farc/commands/{index.go,default.go}` — style template for `cmd/hls_server`.
- `internal/api/testutil_test.go` — fixture pattern to recreate locally for `hlsclient`/`tocindex` tests.
- `go.mod` — confirms `bluenviron/mediacommon/v2` and `gorilla/websocket` are already dependencies.
- `$GOMODCACHE/github.com/bluenviron/mediacommon/v2@v2.9.2/pkg/formats/fmp4/{init.go,init_track.go,sample.go}` and `.../pkg/formats/mp4/codecs/h264.go` — exact muxing API for phase 4.
- `taskfile.yaml` — needs `build/hls_server` (phase 8); ignore `run`/`help`/`db/*`/`env/*` (unrelated leftovers, per `CLAUDE.md`).
- `temp/mediamtx-1.19.3/internal/playback/{muxer_fmp4.go,segment_fmp4.go}` — gitignored, a separate Go module, **not importable**; study-only reference for phase 3/4 muxing patterns.
