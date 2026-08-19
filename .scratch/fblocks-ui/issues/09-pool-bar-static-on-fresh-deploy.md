# Pool status bar stayed static for several minutes on a fresh `docker compose up --build`, cause unconfirmed

Status: inconclusive — could not reproduce; restarting `farc` + restarting
continuous recording made it work correctly, and it has kept working since

## Symptom

2026-08-17, after issue 08's fix was already deployed (confirmed: the
running binary contained `storageengine: write job already failed`, i.e.
the fixed code, not a stale build). User ran `docker compose up --build`
against a real camera (5 continuous channels + 1 event channel, all
pointing at the same `rtsp://.../channel=1` URL, on Storage
`4c0f3cd18cedf1f1`, `fchunk_size=4194304`, `flush_timeout_ns=5000000000`,
matching ADR-017 defaults). `connected: true` for all channels. Fblock 0
went `in_progress`. The pool-status-list bar showed one small static blue
segment and never grew.

## What was ruled out

- Live-tree WS for fblock 0 (`GET .../fblocks/0/tree/ws`) showed the
  in-memory tree actively growing (~42.5MB → ~43.3MB over 22s) — frames
  were genuinely arriving and being filled into the segment's tree. So
  ingest itself was fine.
- Raw disk bytes for the storage's `.img` file, sampled 3× over 90+ seconds
  via `docker run -v farc_farc_data:/data busybox dd | od`, stayed at
  exactly the same non-zero byte count the whole time — meaning
  `internal/storageengine`'s periodic flush (ADR-017) never fired even
  once, despite `AddFrames` clearly succeeding continuously (confirmed via
  the tree growth above — if `Append` had started returning
  `ErrWriteJobFailed`/marking the fblock `Bad`, per issue 08's fix, the
  fblock's state would have flipped to `bad`, which it hadn't).
- Static review of `internal/storageengine/engine.go`'s flush-scheduling
  path (`flushTriggerReadyLocked`/`applyFlushLocked`/`nextDeadlineLocked`/
  `waitUntilLocked`/`Run`) found nothing wrong — the logic correctly
  computes a 5s deadline independent of bitrate and wakes on it via
  `time.AfterFunc` + `sync.Cond`.
- `u.params.FlushTimeoutNS` was confirmed to genuinely be `5s` in memory,
  not silently `0`: `storage.Open` (used for both fresh-Init and reopen)
  always rebuilds `Unit.params` via `readHeader`→`fblock.DecodeParams`,
  which defaults an absent/zero `flush_timeout_ns` to `DefaultFlushTimeoutNS`
  — even though `storage.Init`'s own bootstrap write uses `EncodeParams`
  directly (no such default, so a zero value would be silently omitted
  from the on-disk JSON). Confirmed by decoding the actual on-disk header
  bytes: `flush_timeout_ns:5000000000` was present.
- Pool occupancy (the `occupied > 1` ⇒ `timeout = 0` ADR-017 backlog rule
  in `segment.go`'s `promoteLocked`) was ruled out: the pool-status-list
  bar itself showed only one occupied slot the whole time, and the
  continuous group's segment was promoted (pool went from empty to 1)
  well before the one manual event-channel trigger in this session — a
  later reservation by a different segment can't retroactively change an
  already-promoted job's `timeout`.

## What was NOT reproduced

Added temporary `log.Printf` instrumentation to
`internal/storageengine/engine.go` (`Append`, `flushTriggerReadyLocked`,
`stepWriteLocked`'s caught-up branch, `nextDeadlineLocked`) and rebuilt.
Two live attempts to reproduce the stall from a clean state both failed —
flushing worked correctly every time:

1. Restarting the `farc` container and re-issuing `POST
   /channels/{id}/recording/start` for the same 5 channels on the *same*
   (now `bad`-marked-fblock-0, since the previous in-progress fblock from
   an unclean-shutdown restart gets marked `Bad` by `ConsistencyCheck`)
   Storage: flushed every ~1.5s (fchunk_size-driven, high aggregate
   bitrate — ~2.8MB/s), confirmed via both the debug logs and raw disk
   bytes growing from ~190KB to ~2MB of non-zero content in the sampled
   region.
2. A brand-new Storage (`repro2`, same geometry/params) with first 2, then
   5, continuous channels against the same camera, plus an event-channel
   trigger partway through (mirroring the original session's action
   sequence as closely as possible): flushed every ~1.5-5s throughout,
   including immediately after the event trigger. No stall at any point.

Real aggregate bitrate in both repro attempts (~2.8-8.4 MB/s) turned out
much higher than an early back-of-envelope estimate from the live-tree
JSON growth rate in the original stalled session (~57KB/s) — tree JSON is
far more verbose than raw encoded content, so that estimate wasn't a
reliable proxy for actual bitrate, but it does mean the original session's
real content growth rate (whatever it was) is not independently known.

## Current state

Debug instrumentation was fully reverted (`git checkout --
internal/storageengine/engine.go`, confirmed via `grep -rn DEBUGTMP` and a
clean `go build ./...`), `farc` was rebuilt from the clean fixed source and
restarted, recording was restarted on channels 1-5, and the pool bar has
been confirmed growing live since (`content_size` 50.8MB → 55.0MB in 0.5s
via a direct WS check) — issue 08's fix itself is not in question, it's
independently verified working in this exact deployment, twice over.

## Open question for a future occurrence

Root cause of the one observed stall is unknown. Candidates not yet ruled
out: a narrow startup race specific to the very first `EnqueueOpenWrite`/
`Engine.Run` goroutine scheduling in that one process's lifetime (couldn't
be forced to reproduce); something specific to how the *web UI* created
that storage/those channels (vs. this investigation's raw `curl` calls,
which used the exact same on-disk params); or an environmental hiccup
(e.g. CPU scheduling during the `--build` step) unrelated to farc's code
at all. If this recurs: **do not restart farc first** — capture
`docker compose logs farc`, the pool-WS snapshot, and the live-tree WS
snapshot while it's still stuck, so the actual stuck state can be inspected
instead of losing it to a restart.
