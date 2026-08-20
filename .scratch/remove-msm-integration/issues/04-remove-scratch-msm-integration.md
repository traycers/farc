# 04 — Remove .scratch/msm-integration/

Status: resolved

## Task

Delete `.scratch/msm-integration/` entirely, including its one issue
file `issues/01-audio-vaa-blocks.md` (already `status: fixed`).

Unlike `.scratch/dependency-upgrade/`, which documents changes that stay
in this repo, `.scratch/msm-integration/` documents code that's leaving
it entirely — keeping it around after the code is gone would be a
design-scratch file about a feature that no longer exists here.

Leave every other `.scratch/**` directory untouched, even the ones with
incidental one-line mentions of `internal/msmd`/`internal/vaablocks` as
context for unrelated work (e.g. some `architecture-review`,
`hls-toc-bootstrap`, `rtsp-reconnect` issue files) — those aren't about
msm itself, no action needed on them.

## Verify

`git status` shows `.scratch/msm-integration/` gone and nothing else
under `.scratch/` touched by this issue.

## Comments

2026-08-20: Deleted via `git rm -r .scratch/msm-integration/`. No other
`.scratch/**` directory touched.
