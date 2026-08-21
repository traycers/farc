# Rename hls_server binary to hlsd

Status: implemented (2026-08-21, via `/mattpocock-skills:tdd`) — see `issues/01`, fixed

## Problem

Raised as a side request during the `live-page` grilling session
(`.scratch/live-page/`), unrelated to that feature other than sharing a
naming convention: the project is moving toward `<name>d` binary names
(`farcd`, and the new `apid` from `.scratch/live-page/`). `hls_server`'s
own internal wiring package is already named `hlsd`
(`internal/hlsd.Hlsd`, per CLAUDE.md's "Code layout" section) — the
binary name is the odd one out. Rename the binary to match.

The user's first suggestion was "hsld", confirmed to be a typo for
`hlsd` (matching the existing `internal/hlsd` package) — not a
deliberately different name.

## Design decision

Rename the `hls_server` binary/service to `hlsd` everywhere it's
referenced as a binary/service/command name, matching the already-
existing `internal/hlsd` package name. `internal/hlsd` itself is
unaffected (it's already correctly named).

## Issues

- `issues/01-rename-hls-server-to-hlsd.md`
