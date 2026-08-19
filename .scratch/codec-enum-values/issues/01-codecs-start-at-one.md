# Codec enum values should start at 1, reserving 0 as "uninitialized"

Status: fixed (2026-08-19, via `/mattpocock-skills:tdd`)

## Problem

`mediatree.Codec*` constants (`mediatree/role.go`) currently start at 0:

```go
CodecH264 uint8 = 0
CodecH265 uint8 = 1

CodecPCM   uint8 = 0
CodecAAC   uint8 = 1
CodecG711A uint8 = 2
CodecG711U uint8 = 3
```

Requested change: renumber both enums to start at 1, reserving 0 as an
explicit "uninitialized" sentinel value, for both the video codec enum and
the audio codec enum.

## Facts gathered during grilling (2026-08-19)

- These values are `uint8` and are the on-disk `value` of `RoleCodecVideo`
  (code 10) / `RoleCodecAudio` (code 24) tree nodes — persisted inside
  every fblock's content section, not just an in-memory/API convention.
- `docs/docs/archive/07-media-tree.md` §3.2/§3.3 documents the current
  numbering explicitly as spec ("`h264` = 0, `h265` = 1" /
  "`pcm` = 0, `aac` = 1, `g711a` (PCMA) = 2, `g711u` (PCMU) = 3").
- `docs/docs/archive/07-media-tree.md` §3's "append-only, never renumbered
  or reused" discipline is stated for **Role codes** (the tree's node-type
  numbering), not for the separate `Codec*` value space — there is no
  existing doc statement committing the codec *values themselves* to that
  same discipline, but they are still real on-disk data today.
- Callers that branch on these constants (`internal/ingest/rtsp.go`,
  `internal/msmd/params.go`, `internal/fcontainer/params.go`,
  `internal/segment/init.go`, `internal/vaablocks/streamconfig.go`) all
  compare against the named `mediatree.Codec*` constants, not raw
  literals — a renumber is a recompile-and-go change for all of them, no
  logic changes needed.
- `internal/fcontainer/params.go`'s `ValidateParams` already rejects any
  `CodecVideo`/`CodecAudio` value that isn't one of the named constants
  (`errInvalidVideoCodec`/`errInvalidAudioCodec`) — after a renumber, an
  unset (zero-value) `Params.CodecVideo`/`CodecAudio` field would already
  be rejected by this existing check with no code change, since 0 would no
  longer match any valid constant.
- The frontend already has a raw-value lookup table that hardcodes the
  current numbering: `web/src/pages/fblockTreeFormat.ts`'s
  `VIDEO_CODECS`/`AUDIO_CODECS` maps (`'0': 'H264'`, `'0': 'PCM'`, etc.) —
  built during `.scratch/fblocks-ui/issues/
  12-tree-codec-frame-kind-and-precise-time.md`.
- There is a real dev deployment (192.168.71.65) with actual recorded
  fblocks on disk today, encoding the *current* numbering
  (`CodecH264 = 0` etc.) as real persisted bytes.
- `docs/docs/archive/03-storage-format.md` §on format versioning: a
  `format_version_major` bump is the documented mechanism for
  *incompatible* on-disk changes ("библиотека отказывается работать с
  хранилищем несовместимой мажорной версии"); a `format_version_minor`
  bump is for backward-compatible extensions only.
- `internal/fcontainer/filler_test.go`'s "bad codec" test cases use the
  literal `99` as an already-invalid sentinel — unaffected by renumbering
  either way.
- No binary/golden test fixtures anywhere in the repo bake in raw encoded
  fblock/content bytes with a codec value outside of a Go constant
  reference — every existing test constructs fixtures via named constants
  at run time, so renumbering doesn't silently corrupt any test's meaning.
- Existing naming precedent for this exact pattern: `fblock/catalog.go`'s
  `Uninitialized State = 0` is the zero value of the (unrelated) fblock
  lifecycle `State` enum, with the same "0 means not yet set" meaning —
  supports naming the new sentinel `CodecUninitialized`.

## Design decisions (grilling, 2026-08-19)

- **Renumbering**: `CodecH264=1, CodecH265=2` (video);
  `CodecPCM=1, CodecAAC=2, CodecG711A=3, CodecG711U=4` (audio).
- **Breaking change, no migration**: this changes the on-disk meaning of
  a persisted byte. Accepted as a breaking change — no
  `format_version_major` bump, no version-gated decode path. Any
  already-recorded fblock (including the real dev deployment at
  192.168.71.65) will, after this ships, decode its old `codec=0` as
  `CodecUninitialized` rather than the `CodecH264`/`CodecPCM` it actually
  was; dev/test storage gets wiped and re-recorded as needed.
- **One shared sentinel**: `mediatree.CodecUninitialized uint8 = 0`,
  reused for both `RoleCodecVideo` and `RoleCodecAudio` fields — same
  "not set" meaning regardless of domain, matching the
  `fblock.Uninitialized State = 0` naming precedent. No separate
  `CodecVideoUninitialized`/`CodecAudioUninitialized`.
- **Append-only from now on**: extend `mediatree/role.go`'s existing
  "codes are append-only, never renumbered/reused" doc comment (currently
  scoped to `Role` codes) to also cover the `Codec*` value space — this
  ticket is itself the one-time exception that motivates the rule.
- **Web tree viewer**: `web/src/pages/fblockTreeFormat.ts`'s
  `VIDEO_CODECS`/`AUDIO_CODECS` maps get their keys shifted to the new
  values, plus an explicit `'0': 'uninitialized'` entry in both maps
  (rather than falling through to the generic `unknown(0)`).
- **No Plan Mode** — straight to `/mattpocock-skills:tdd`.
- **Testing**: unit tests only. Go: `internal/fcontainer` (new codec
  values, `CodecUninitialized` rejected by existing validation). Web:
  `fblockTreeFormat.test.ts`/`FblockTreePage.test.tsx` (new codec values +
  the `'uninitialized'` label for `0`). No new e2e scenario.

## Fix

- `mediatree/role.go`: added `CodecUninitialized uint8 = 0`, shared by
  both enums. Renumbered `CodecH264=1, CodecH265=2` (video) and
  `CodecPCM=1, CodecAAC=2, CodecG711A=3, CodecG711U=4` (audio). Both const
  blocks' doc comments now state the values are append-only (like `Role`
  codes), never renumbered/reused again.
- No change needed to `internal/fcontainer/params.go`'s `Validate` — its
  existing "must be one of the named constants" switch already rejects
  `CodecUninitialized`/any other value with no code change, exactly as
  decided during grilling.
- `docs/docs/archive/07-media-tree.md`: §3.2/§3.3 codec value tables
  updated to the new numbers plus a note that 0 is `CodecUninitialized`
  and never written to disk deliberately; §6 (Открытые вопросы) records
  the decision (breaking change, no migration, append-only from now on).
- `web/src/pages/fblockTreeFormat.ts`: `VIDEO_CODECS`/`AUDIO_CODECS` keys
  shifted to the new numbers, plus an explicit `'0': 'uninitialized'`
  entry in both maps (distinct from the generic `unknown(N)` fallback).
- No changes needed anywhere else: every other call site
  (`internal/ingest/rtsp.go`, `internal/msmd/params.go`,
  `internal/segment/init.go`, `internal/vaablocks/streamconfig.go`) only
  ever compares against the named `mediatree.Codec*` constants, never raw
  literals.

## Tests

TDD red→green:
- `internal/fcontainer/filler_test.go`'s `TestStreamParamsValidation`
  table gained two cases: `CodecVideo`/`CodecAudio` left at their Go
  zero-value (`mediatree.CodecUninitialized`) must fail validation.
  Compile-red first (constant didn't exist yet), then green once
  `mediatree/role.go` was updated — proves the actual bug fixed here:
  before this change, an unset `uint8` codec field silently read as a
  real `CodecH264`/`CodecPCM` and passed validation.
- `web/src/pages/fblockTreeFormat.test.ts`: updated expectations to the
  new numbering, added cases for `'0'` → `'uninitialized'` on both
  `codec(video)` and `codec(audio)`. Red (assertions against old numbers)
  confirmed before updating `fblockTreeFormat.ts`.
- `web/src/pages/FblockTreePage.test.tsx`: updated its one codec fixture
  from value `'1'` (old H265) to `'2'` (new H265) — caught as a real red
  (wrong-value assertion, not a compile error) after the renumbering.

Full verification: `go build ./...`, `go test ./...` (all packages
green), `cd web && npx vitest run` (20 files / 100 tests green),
`cd web && npx tsc --noEmit` (clean).
