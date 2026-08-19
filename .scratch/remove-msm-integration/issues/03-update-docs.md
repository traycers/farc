# 03 — Update CLAUDE.md, CONTEXT.md, design docs, and PLAN.md

Status: open
Blocked by: 01

## Task

- **`CLAUDE.md`**: this needs real rewriting, not just deletion.
  - The "Project" paragraph currently lists `msm_server` as one of
    "Three binaries" — becomes two (`farcd`, `hls_server`).
  - The whole msm/controller-integration paragraph in the Architecture
    section (inbound `archivesapi`/`farcctl`, outbound
    `msmd`/`msmclient`/`msmapi`/`vaablocks`) — remove entirely. Also
    remove the "External controller/msm integration" heading and the
    2026-08-13 decision note it references, since that decision (moving
    archives-serving out of farcd into msm_server) is now moot — the
    integration isn't in this repo at all anymore. Keep whatever's left
    of the architecture description internally consistent (e.g. don't
    leave a dangling reference to "the sole integration point" once
    there's no integration point left in this repo).
  - Commands section: remove the `go build -o msm_server
    ./cmd/msm_server` / `task build/msm_server` line, and drop
    `msm_server` from "Build everything".
  - Code layout section: remove the `cmd/msm_server/` bullet, remove
    `msmconfig` from the `config`/`hlsconfig`/`msmconfig` bullet (leaving
    just `config`/`hlsconfig`), remove the `msmclient`/`msmapi`/
    `vaablocks`/`msmd` bullet, remove the `farcctl`/`archivesapi` bullet.
    Fix the `internal/api` bullet's aside about the `archives` route
    group having "moved to msm_server's `internal/archivesapi`" — that
    route group doesn't exist anywhere in `farc` anymore, not just moved.
- **`CONTEXT.md`**: delete the `### msm_server / integration` section
  entirely (ВАА-блок/VAA-block, "msm_server как имитация," fblocks_add/
  fblocks_del, params_add, etc.), plus the scattered mentions in the
  system-overview paragraph, the Consumer definition, the best-effort
  delivery-policy note, and the glossary "avoid" note explaining
  "archive"/"архив" is an msm/controller-API synonym for Storage — all
  three of the latter should be reworded or removed depending on whether
  they still make sense once the section itself is gone (re-read each in
  context rather than assuming a blanket removal is right for all of
  them).
- **`docs/agents/domain.md`**: line 7 currently describes `CONTEXT.md`'s
  glossary as covering "msm_server integration terms (VAA-block, stream
  config versions, the fblocks_add/vaa_blocks_add/info_set reporting
  sequence)" — update this description once that glossary section is
  gone.
- **`docs/docs/archive/02-storage.md`**: remove the one sentence noting
  hls_server/msm_server also got their own `/metrics` the same way farcd
  did — reword to just describe farcd's/hls_server's `/metrics`.
- **`docs/docs/archive/11-service-composition.md`**: remove the two
  sentences mentioning msm_server (comparing `internal/msmd`'s
  RTSP-reconnect backoff style, and `/metrics` parity) — check the
  surrounding paragraphs still read coherently once those sentences are
  gone.
- **`PLAN.md`**: remove the msm_server mentions in Phase 26's
  `client_golang`/observability paragraph and in the deploy-artifacts
  table's `Dockerfile.msm_server` row. Add a new phase entry (next
  number after whatever's currently last) documenting this removal once
  it actually lands — what was deleted, where it went (new separate
  repo), and pointing at this `.scratch/remove-msm-integration/` effort
  for detail, matching how Phase 26 pointed at
  `.scratch/dependency-upgrade/`.
- No ADR changes (decided against during grilling — see spec.md).

## Verify

`mkdocs build -f docs/mkdocs.yaml` inside the docs container (per this
repo's own convention — don't run mkdocs locally) to confirm no broken
internal links/references after editing `02-storage.md`/
`11-service-composition.md`. Read back `CLAUDE.md`/`CONTEXT.md` fully
once done to check for dangling references to msm/archivesapi/farcctl/
vaablocks/msmclient/msmapi/msmconfig/msmd left anywhere (`/usr/bin/grep
-il msm CLAUDE.md CONTEXT.md docs/agents/domain.md` — remember the
aliased `grep` skips these files, see issue 01's note).

## Comments
