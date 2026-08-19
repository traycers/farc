# 03 — mediacommon/v2 latest + restore codecs.* API

Status: resolved
Blocked by: 01

## Task

`go get github.com/bluenviron/mediacommon/v2@v2.9.3` (check for newer).

Phase 23 downgraded this to v2.1.0 because `pkg/formats/mp4/codecs`
(added in v2.6.0) needs go1.24, and had to use `fmp4.CodecH264`/
`fmp4.CodecMPEG4Audio` and `fmp4.PartTrack.Samples []*Sample` instead.
That constraint is gone now (issue 01 lands go1.26). Revert:

- `internal/segment/init.go`, `internal/segment/media.go`: swap
  `fmp4.CodecH264`/`fmp4.CodecMPEG4Audio` back to
  `codecs.H264`/`codecs.MPEG4Audio` (new import
  `github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs`), and
  `fmp4.PartTrack.Samples`'s type back from `[]*Sample` to whatever
  `[]*PartSample` maps to at v2.9.3 — check the actual v2.9.3 API before
  assuming it's a straight revert, the package may have moved again
  across 8 minor versions.

## Verify

`go build ./...`, `go test ./internal/segment/... -race`. Also re-check
`internal/segment`'s existing tests still assert against the same sample
byte content — a codec-type refactor here is exactly the kind of change
that can silently pass wrong data through if the tests only check
lengths/counts rather than actual bytes.

## Comments

Landed 2026-08-19. The version bump itself came for free as a side
effect of issue 02 (`go get gortsplib/v5` pulled `mediacommon/v2` to
v2.9.3 transitively).

Reality differed from this ticket's assumption: at v2.9.3,
`fmp4.CodecH264`/`fmp4.CodecMPEG4Audio`/`fmp4.PartSample` turned out to
be **type aliases** for `codecs.H264`/`codecs.MPEG4Audio`/`fmp4.Sample`
(`type CodecH264 = codecs.H264`, etc.) — not a distinct workaround shape
— so `internal/segment/{init,media}.go` already compiled unchanged.
Switched them to the direct `codecs.H264`/`codecs.MPEG4Audio` names
anyway (in `init.go` and `segment_test.go`) to actually honor the
decision rather than silently ride the alias.

`golangci-lint`'s `staticcheck` linter caught real deprecations this
version bump introduced, fixed inline rather than left as warnings:
- `media.go`: `Sample.FillH264(...)` (deprecated, "removed in next
  version") replaced with its own inlined body —
  `h264.AVCC(au).Marshal()` + `h264.IsRandomAccess(au)` — and
  `fmp4.PartSample` → `fmp4.Sample` (the non-deprecated name; still an
  alias so this is style-only, no behavior change).
- `segment_test.go`: `Sample.GetH264()` (deprecated) replaced with
  `h264.AVCC{}.Unmarshal(s.Payload)` inline; `AudioSpecificConfig.
  ChannelCount` (deprecated) → `.ChannelConfig` — confirmed the test
  fixture's actual bytes still assert to `1` under the new field name
  (test passes, not just compiles), so this isn't a silent behavior
  change.

`go build`/`go vet`/`golangci-lint run ./internal/segment/...` (0
issues) / `go test ./internal/segment/... -race` and the full
`go test ./... -race` all green.
