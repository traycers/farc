# Fblock tree should show human-readable codec/frame-kind text and microsecond-precision timestamps

Status: fixed (2026-08-18, via `/mattpocock-skills:tdd`)

## Problem

- The tree view renders codec and frame-kind fields as raw decimal bytes:
  video shows `codec(video) (uint8) = 0`, audio shows `codec(audio) (uint8)
  = 1`. The user suspected `0` might be a nil/unset sentinel value.
- `frame_kind` (video-only, whether a frame is a keyframe) is likewise
  shown as a raw decimal (`73`/`80`) instead of the `I`/`P` letters the
  byte actually encodes.
- Separately: tree timestamp nodes render via `nsToDisplayString`, which
  truncates to whole seconds. Two frames close together (typical at video
  framerate) end up showing identical times in the tree — real
  information loss in what's meant to be a precise debugging view.

## Facts gathered during grilling

- `mediatree/role.go`: video codec constants are `CodecH264 uint8 = 0`,
  `CodecH265 uint8 = 1`; audio codec constants are `CodecPCM uint8 = 0`,
  `CodecAAC uint8 = 1`, `CodecG711A uint8 = 2` (PCMA), `CodecG711U uint8 =
  3` (PCMU, "never observed in a real capture; assigned by analogy"). No
  `CodecNone`/`Unknown` sentinel exists in either enum, and no
  `Codec.String()`/name-lookup exists anywhere in the codebase — **video
  codec 0 is a real value (H264), not nil**; same for audio codec 1
  (AAC). This is purely a missing display decode, not a data/nil bug.
- `FrameKindI uint8 = 0x49` ('I'), `FrameKindP uint8 = 0x50` ('P') —
  deliberately chosen as ASCII codes; written per-video-frame in
  `internal/fcontainer/filler.go:230`. Already present in the TOC and
  already reaches the frontend's JSON as a child `frame_kind` node, just
  never decoded from its raw decimal ASCII value.
- `internal/api/fblocktree.go`'s `formatNodeValue()` does a generic
  `strconv.FormatUint` for any `TypeUint8` node, regardless of role — this
  single function feeds both the static HTTP tree endpoint
  (`handleReadFblockTree`) and the live WS tree endpoint
  (`handleFblockLiveTreeWS`), so a frontend-only fix covers both without
  touching the backend.
- `web/src/api/ns.ts`'s `nsToDisplayString` goes through `nsToDate`
  (`Number(ns / 1_000_000n)` into a JS `Date`), which has a millisecond
  ceiling — it cannot express microseconds without separately handling the
  sub-millisecond remainder.

## Design decisions (grilling, 2026-08-18)

- Decode codec/frame_kind in the **frontend only**, no backend change: a
  new pure module `web/src/pages/fblockTreeFormat.ts` (+ `.test.ts`)
  exporting `formatDecodedValue(role, value): string`, wired into
  `FblockTreePage.tsx`'s existing `formatValue`, alongside its current
  `type === 'timestamp'` special case.
- Decoded text: video codec → `H264`/`H265`; audio codec → `PCM`/`AAC`/
  `PCMA`/`PCMU`; `frame_kind` → `I`/`P`.
- Unknown/out-of-range value → `unknown(N)`, keeping the raw number
  visible — this is a diagnostic tool, must not hide anomalies silently.
- Scope limited to exactly these three roles (`codec(video)`,
  `codec(audio)`, `frame_kind`) — no audit of other numeric roles in this
  ticket; a separate ticket if more turn up later.
- The node label's `(uint8)` type suffix is kept as-is for these fields,
  for consistency with every other tree node (e.g. `codec(video) (uint8) =
  H264`).
- Timestamp precision: new `nsToDisplayStringPrecise(ns): string` in
  `web/src/api/ns.ts`, format `HH:MM:SS.ffffff` (6-digit microseconds
  computed from `ns % 1_000_000_000n`, not through `Date`). Used **only**
  for `type === 'timestamp'` tree nodes (`FblockTree`/`formatValue`) — the
  container-level begin/end header on `FblockTreePage.tsx` (lines 73-74)
  and the other 3 call sites of `nsToDisplayString`
  (`FblocksListPage.tsx`, `PlayerPage.tsx`) are unaffected; second-level
  precision is enough there.
- No plan mode needed — straight to `/mattpocock-skills:tdd`.
- No new e2e scenario — existing component/unit tests plus a regression
  run of the 4 existing Playwright specs is enough; this is a pure display
  fix over data the real stack already exercises.

## Fix

- `web/src/pages/fblockTreeFormat.ts` (new): `formatDecodedValue(role,
  value)`, three lookup tables (`VIDEO_CODECS`, `AUDIO_CODECS`,
  `FRAME_KINDS`), `unknown(N)` fallback for any role/value not covered.
- `web/src/api/ns.ts`: new `nsToDisplayStringPrecise(ns)` — reuses
  `nsToDisplayString` for the whole-second part, appends
  `.` + 6-digit zero-padded microseconds computed from
  `ns % 1_000_000_000n / 1_000n` (bypasses `Date`'s millisecond ceiling).
- `web/src/pages/FblockTreePage.tsx`: `formatValue` (now exported) tries
  `timestamp` → `nsToDisplayStringPrecise` first, then falls through to
  `formatDecodedValue(node.role, node.value)` for any other node with a
  value. The container-level begin/end header (unrelated lines) still uses
  the plain `nsToDisplayString`, untouched.

## Tests

TDD red→green, all seams as planned:
- `web/src/pages/fblockTreeFormat.test.ts`: one case per enum member
  (video codec ×2, audio codec ×4, frame_kind ×2) plus one unknown-value
  case per role (11 tests total).
- `web/src/api/ns.test.ts`: exact microsecond digits, zero-padding under 6
  digits, and a direct demonstration of the bug this fixes (two ns values
  1000ns apart, same second, produce different `nsToDisplayStringPrecise`
  output).
- `web/src/pages/FblockTreePage.test.tsx` (new): exercises the exported
  `formatValue` directly — codec(video)/frame_kind decoding, an unrelated
  uint8 role falling through to `undefined` (raw value stays visible), and
  a timestamp node rendering with microsecond precision distinguishing two
  close frames.

`npx vitest run` (98 tests, whole `web/` suite), `npx tsc --noEmit`, and
`npm run build` all clean.

## Explicitly out of scope

- Any other numeric role in the tree beyond `codec(video)`/
  `codec(audio)`/`frame_kind`.
- Changing `nsToDisplayString`'s existing 4 call sites.
- Backend changes of any kind.
