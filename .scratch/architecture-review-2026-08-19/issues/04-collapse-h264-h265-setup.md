# Collapse `setupH264`/`setupH265` into one gated pipeline

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — test-suite parameterization (see original "Tests to consider") deliberately deferred, see Comments

## Problem

`internal/ingest/rtsp.go:197-308`: `setupH264` and `setupH265` are ~55-line
near-duplicates. Decoder setup, `keyframeGate`, `gopShedGate`, param-change
detection, and `HandleFrame` dispatch/error-logging are copy-pasted between
them, differing only in codec-specific NALU parsing. `rtsp_keyframegate_test.go`
and `rtsp_gopshedgate_test.go` each have an H264 test and an H265 test
explicitly commented "mirrors the H264 test above" — every gating feature
gets written and tested twice.

## Design (settled via grilling, 2026-08-19)

A small interface, `videoCodecStrategy` (`decode`, `initialParams`,
`classify`), won out over a struct-of-closures (rejected: sps/pps/vps
mutation between calls would need to be threaded through captured
variables anyway — a struct with methods reads more directly as "codec
state + how to interpret it"). `keyframeGate`/`gopShedGate` construction
stays shared, inside the one pipeline function. `h264Strategy`/
`h265Strategy` each own their decoder + current sps/pps/(vps) and
implement `classify` independently — H265's whole-AU `h265.IsRandomAccess`
IDR detection vs. H264's per-NALU-type switch is a genuine codec
difference, deliberately not unified.

## Fix (2026-08-19)

- `internal/ingest/rtsp.go`: new `videoCodecStrategy` interface + shared
  `(ci *ChannelIngest) setupVideo(c, medi, f, strategy, streamNum)` holding
  the keyframe-gate/GOP-shed/dispatch pipeline (previously duplicated in
  full). `h264Strategy`/`h265Strategy` implement `decode`/`initialParams`/
  `classify`, each preserving its original codec-specific logic verbatim.
  `setupH264`/`setupH265` reduced to: create the decoder, build the
  strategy, call `setupVideo` — same signatures as before, since all
  existing tests call them directly.
- No new tests needed for the extraction itself: all 7 existing
  `TestChannelIngest_H264_*`/`TestChannelIngest_H265_*` tests
  (`rtsp_keyframegate_test.go`, `rtsp_gopshedgate_test.go`,
  `rtsp_paramscompare_test.go`) already drive `setupH264`/`setupH265`
  through real gortsplib RTP encode/decode and passed unchanged, confirming
  no behavior change.
- Full `go test ./...` green, `go test -race ./internal/ingest/...` green,
  `golangci-lint run ./internal/ingest/...` shows 0 issues. `gofmt -l`
  clean.

## Comments

Deferred: the original ticket's "Tests to consider" suggested collapsing
the mirrored H264/H265 test pairs into one parameterized test per gating
feature. Not done in this pass — coverage between the two codecs turned
out to already be asymmetric (`LogsCountOfDroppedLeadingFrames`,
`WarnsIfNoKeyframeArrivesWithinTimeout`, and
`RepeatedIdenticalInBandSPSPPS_NoNewConfig` currently have no H265
counterpart at all), so parameterizing would mean deciding whether to add
that missing H265 coverage too — a separate, own-scope piece of work, not
a mechanical test refactor. Worth its own ticket if wanted.
