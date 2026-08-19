# Remove msm/controller integration from farc

Status: open — grilled 2026-08-19, not yet implemented (next step:
implement each issue, likely via a separate `/tdd` pass like the
dependency-upgrade effort)

Reached via `/grilling` on 2026-08-19. The msm/controller integration
(currently the `msm_server` binary and its supporting packages) is moving
to a new, separate repository outside `farc`. This effort removes it
cleanly from this repo — it does not build or seed the new repository.

## Decisions (settled during grilling)

1. **Destination**: a new, separate repository. This repo only deletes;
   producing an export/copy for the new repo is out of scope here.
2. **No git-history preservation** — plain deletion, no `git subtree
   split`/`git filter-repo`. History stays discoverable via this repo's
   own git log if ever needed as a reference.
3. **Scope of deletion**, confirmed via a full-repo dependency scan
   (2026-08-19, see issue 01 for the exact file list — ~4660 lines):
   - `cmd/msm_server/`
   - `internal/msmd/`
   - `internal/msmclient/`
   - `internal/msmapi/`
   - `internal/msmconfig/` (missed in the original ask, caught by the
     scan — msm_server's env-only process config)
   - `internal/archivesapi/`
   - `internal/farcctl/`
   - `internal/vaablocks/`
   - `Dockerfile.msm_server`
   Confirmed nothing outside these packages imports any of them — the
   dependency edge only goes one way (msm packages import core farc
   packages as a library, not vice versa) — so this is a self-contained
   deletion with no ripple into `internal/farcd`, `internal/api`,
   `internal/hlsd`, `cmd/farc`, `cmd/hls_server`, etc.
4. **`internal/hlsclient` stays untouched** — it's hls_server's own
   farcd HTTP/WS client (used by `internal/hlsapi`, `internal/hlsd`,
   `internal/segment`, `internal/tocindex`); `msmd` merely reuses it, it
   doesn't own it.
5. **Shared farcd events/fields stay untouched** even though their doc
   comments mention msm_server as a consumer: `EventFblockDeleted`
   (`internal/storage/writetxn.go`, also consumed by
   `internal/tocindex/subscriber.go`, `internal/segmentcache/s3.go`, and
   the web UI's fblocks grid) and `JournalEvent.Storage` on the
   recording-started/stopped events (`internal/farcd/farcd.go`, also
   consumed by the web UI's Journal page, asserted on by
   `e2e/tests/journal.spec.ts`). Only reword the doc comments that cite
   msm_server as *the* reason these exist — don't touch the fields/events
   themselves.
6. **`DELETE /storages/{id}` on farcd — KEPT, not deleted**, even though
   archivesapi/farcctl were its only caller. It gets a second, unrelated
   real caller: see the separate `storage-detach-button` feature-slug
   (not part of this effort — surfaced during this grilling session but
   explicitly not related to the msm removal). This effort must still
   update `handleRemoveStorage`'s doc comment
   (`internal/api/storages.go`), which currently says "the only expected
   caller (archivesapi)" — that becomes false once archivesapi is deleted
   regardless of whether the detach-button work has landed yet.
7. **`GET /channels?storage=` on farcd — left exactly as is**, no
   deletion, no doc changes, no new caller wired up. Harmless read-only
   filter param; no clear UI use case right now.
8. **No new ADR** for this decision (explicitly declined during
   grilling).
9. **Documentation to update** (not just code):
   - `CLAUDE.md` — the whole msm/controller-integration paragraph in
     Architecture, the `msm_server` build command, and every
     `msmconfig`/`archivesapi`/`farcctl`/`msmclient`/`msmapi`/`vaablocks`/
     `msmd` entry in Code layout.
   - `CONTEXT.md` — delete the `### msm_server / integration` glossary
     section entirely (VAA-block, fblocks_add/fblocks_del, params_add,
     etc.), plus the scattered mentions in the system-overview/Consumer/
     best-effort-delivery-policy sections and the "archive"/"архив"
     glossary note that only exists to disambiguate from msm's API term.
   - `docs/agents/domain.md` — line 7's meta-reference to what
     `CONTEXT.md`'s glossary covers currently lists msm_server terms;
     update once that glossary section is gone.
   - `docs/docs/archive/02-storage.md` and `11-service-composition.md` —
     each has exactly one passing sentence about msm_server's `/metrics`
     parity or reconnect-backoff style; remove those sentences, nothing
     else in either file is msm-related.
   - `PLAN.md` — remove msm_server mentions (Phase 26's
     `client_golang`/observability paragraph, the deploy-artifacts
     table's `Dockerfile.msm_server` row), and add a new phase entry
     documenting this removal once it lands.
   - No ADR touches needed (see decision 8).
10. **`.scratch/msm-integration/` — delete entirely** (its one issue,
    `01-audio-vaa-blocks.md`, is msm-specific and already resolved;
    unlike `dependency-upgrade`, which documents changes that stay in
    this repo, this directory documents code that's leaving it).
11. **Build/CI/Docker/observability cleanup**:
    - `taskfile.yaml`: `z_msm_name`/`z_msm_file_name` vars, the
      `build/msm_server` task, and its entry in `build`'s `deps` list.
    - `docker-compose.yaml`: the `msm_server` service (behind the `msm`
      profile).
    - `deploy/docker-compose.release.yaml`: one passing comment
      mentioning msm_server — no actual service defined there, just
      clean the comment.
    - `deploy/observability/prometheus.yml`: the `job_name: msm_server`
      scrape target.
    - `deploy/observability/promtail-config.yaml`: one comment mention.
    - `deploy/observability/grafana/dashboards/{logs,services-overview}.json`:
      job-label regexes referencing msm_server, plus one dedicated panel
      ("msm_server WS connected to farcd" / `msm_server_ws_connected`) in
      `services-overview.json` — remove the panel, not just the label.
    - No CI workflow changes needed: `.github/workflows/ci.yml` has no
      dedicated msm_server step (only implicitly builds/tests it via
      `go build ./...`/`go test ./... -race`, which stops doing so once
      the packages are gone), and `.github/workflows/release.yml` never
      referenced `Dockerfile.msm_server` at all.
    - `.dockerignore` has no msm_server-specific entry to remove
      (pre-existing gap, unrelated).
12. **Implementation timing**: this issue set is the plan only. Actual
    implementation is a separate step (likely a future `/tdd` pass),
    matching how `dependency-upgrade` was handled.

## Not in scope / explicitly decided against

- No export/copy of the deleted code for the new repository.
- No git-history extraction (`subtree split`/`filter-repo`).
- No new ADR.
- No changes to `internal/hlsclient`, `EventFblockDeleted`,
  `JournalEvent.Storage`, or `GET /channels?storage=`.
- The detach-button UI work is tracked separately under
  `.scratch/storage-detach-button/` — not part of this effort.

## Issues

See `issues/01`–`05`. Work roughly in numeric order — later issues
assume earlier ones landed (see each issue's `Blocked by:` line).
