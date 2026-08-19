# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root — the domain glossary, kept current via `/domain-modeling`. Covers system-level terms (Archive, Consumer, Video Gateway, best-effort delivery), Storage/fblock terms (fblock's number/offset-vs-UUID identity, Catalog, write_sequence, StorageEngine, Backpressure levels, Protected), hls_server/playback terms, and msm_server integration terms (VAA-block, stream config versions, the fblocks_add/vaa_blocks_add/info_set reporting sequence).
- **`docs/docs/archive/adr/`** — this repo's actual ADR location, numbered `001`–`021`. This repo does **not** use the skill's default `docs/adr/`; per `CLAUDE.md`, these ADRs "are actively followed, not historical artifacts," so read any that touch the area you're about to work in.
- The numbered design docs `docs/docs/archive/00-requirements.md` … `12-hls-server.md` are the broader architecture spec (storage/fblock format, capture policy, HLS serving, etc.). `CLAUDE.md` already directs agents to read the relevant ones before storage/fblock/hls_server work, so this file doesn't duplicate that instruction — it only covers where the domain glossary and ADRs live for skills that don't already read `CLAUDE.md`.

If `CONTEXT.md` is ever missing (e.g. a fresh checkout before it's been recreated), proceed silently — don't flag its absence or suggest creating it upfront; that's `/domain-modeling`'s job, lazily, as terms actually get resolved.

## File structure

Single-context repo (this repo):

```
/
├── CLAUDE.md                    ← engineering guide; points into docs/docs/archive/
├── CONTEXT.md                   ← domain glossary, maintained via /domain-modeling
└── docs/
    └── docs/archive/
        ├── 00-requirements.md … 12-hls-server.md
        └── adr/                ← ADR-001 … ADR-021 (this repo's real ADR location)
```

There's no `CONTEXT-MAP.md` and no monorepo split (`web/`, `e2e/`, `spec/` each have their own standalone `package.json`, but there's no `pnpm-workspace.yaml`/`workspaces` tying them together) — this stays single-context unless that changes.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts docs/docs/archive/adr/ADR-005 (write priority over reads) — but worth reopening because…_
