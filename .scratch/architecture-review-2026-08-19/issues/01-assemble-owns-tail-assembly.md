# Let `assemble.go` own tail-assembly, instead of three sites re-deriving it

Status: fixed (2026-08-19, via `/mattpocock-skills:grilling` + `/mattpocock-skills:tdd`) — went further than the original proposal: one `assembleTail` unifies all three sites, not just two

## Problem

`internal/storage/assemble.go`'s `assembleFblock` is supposed to be the one
place that knows the fblock tail's on-disk layout — pad content to
capacity, `magic_toc`, TOC bytes, then `CRC32` computed **over the
padded-to-capacity content buffer, not the written bytes**, then epilogue.
Two more sites re-derive this same sequence independently:

- `internal/storage/segment.go`'s `writeTailLocked` (~lines 555-578, after
  the `beginFblockWrite`/`completeFblockWrite` refactor in
  `.scratch/storage-write-transaction/issues/01-...md` — line numbers will
  have shifted slightly, re-check before starting)
- `internal/storage/consistency.go`'s `recoverPartialWrite`

All three must independently know the padding/CRC invariant. This is
exactly the on-disk-format knowledge `assemble.go` exists to encapsulate,
and it leaks.

## Design (settled via grilling, 2026-08-19)

Grilling surfaced a stronger unification than the original proposal: all
three sites — not just two — compute the exact same thing, differing only
in how many of `content`'s leading bytes are already physically on disk.
One function replaces the duplicated logic everywhere:

```go
func assembleTail(content, tocBuf []byte, fblockSize uint64, paramsSize, catalogSize uint32, alignment int, alreadyWritten int64) ([]byte, error)
```

Returns `content[alreadyWritten:] + zeropad + magic_toc + tocBuf +
encodeEpilog(epilog)`, with `epilog.CRC32Content` always computed over the
*entire* padded content regardless of `alreadyWritten`. `alreadyWritten`
unifies all three callers:

- `assembleFblock` (full rewrite from scratch): `alreadyWritten = 0` —
  `assembleFblock` itself became `assembleHeaderAndMagic(...) + assembleTail(..., 0)`.
- `writeTailLocked` (finishing an incremental open write): `alreadyWritten = contentSoFar`.
- `recoverPartialWrite` (crash recovery — recovered bytes already on disk,
  only the trailer needs rewriting): `alreadyWritten = len(content)` (=
  `trailerOff`), which naturally degenerates to no content bytes in the
  returned tail.

## Fix (2026-08-19)

Implemented via TDD:

- `internal/storage/assemble.go`: new `assembleTail`, with `assembleFblock`
  rewritten in terms of it (`assembleHeaderAndMagic` + `assembleTail(...,0)`,
  concatenated).
- New `internal/storage/assemble_test.go` (first direct test file for this
  package's assembly logic), 4 tests written test-first: full content
  (`alreadyWritten=0`), partial (`alreadyWritten>0`, asserting the CRC
  still covers the *entire* content, not just the unwritten remainder),
  `alreadyWritten == len(content)` (recovery's degenerate case — asserts
  no content bytes leak into the tail), and content-exceeds-capacity
  (error path). Expected bytes built independently in each test via
  `fblock.CRC32`/`fblock.EncodeEpilog` directly, not by re-deriving
  `assembleTail`'s own logic.
- `internal/storage/segment.go`'s `writeTailLocked` and
  `internal/storage/consistency.go`'s `recoverPartialWrite` both rewired to
  call `assembleTail` instead of re-deriving padding/CRC/epilogue inline.
- No behavior change: `go test ./...` green, `go test -race
  ./internal/storage/...` green, `golangci-lint run ./internal/storage/...`
  shows only the same pre-existing `noinlineerr` debt as `HEAD` (verified
  via a throwaway worktree) — the new test file's own `prealloc` finding
  was fixed inline. `gofmt -l` clean.

## Comments
