# 06 — update CLAUDE.md and PLAN.md

Status: resolved
Blocked by: 01, 02, 03, 04, 05

## CLAUDE.md

- Line 7 ("Go 1.21, module `gitlab.rigel.bolid.ru/...`"): update the Go
  version to whatever `go.mod` ended up with (issue 01), and fix the
  module path to `github.com/traycers/farc` — it was renamed in Phase 22
  but this line was never updated; opportunistic fix, unrelated to this
  upgrade but touching this line anyway.
- Grep the rest of the file for any other stale version-specific mention
  before finishing (e.g. anything implying gorilla/mux, or old
  gortsplib/mediacommon version numbers) — none were found during the
  grilling session's initial read, but re-check since the file may have
  changed since.

## PLAN.md

- Delete Phase 23's entry entirely. Its whole premise (staying on
  go1.21 to keep `gortsplib` v4/`mediacommon` v2.1.0/avoid the
  gorilla/mux-forcing stdlib change) is reversed by this work, and the
  two concrete findings it documents (the `playlist.go` loop-variable
  aliasing bug, the `net/http`→`gorilla/mux` port) are both being
  reverted/no longer relevant as written.
- Note in the deleted phase's checklist item slot (renumbering isn't
  required — other phases already skip numbers, e.g. 24 appears before
  23 in the current file) that decision was superseded; then add a new
  phase entry (next available number) summarizing this upgrade: Go
  1.21→(new version), `gortsplib` v4→v5, `mediacommon/v2` v2.1.0→v2.9.3,
  `gorilla/mux` removed in favor of stdlib `net/http.ServeMux`, web
  majors (React 19/router 7/Vite 8/TS 7), plus which verification levels
  were actually run (issue 07).
- Check the "Critical files" section near the bottom for any reference
  to `gorilla/mux`-specific behavior that needs rewording now that it's
  gone.

## Comments

Landed 2026-08-19. `CLAUDE.md` line 7: "Go 1.21" → "Go 1.26", module path
`gitlab.rigel.bolid.ru/...` → `github.com/traycers/farc` (Phase 22's
rename, never updated here). Note: `CLAUDE.md` turned out to be
`.gitignore`d in this repo (not tracked in `HEAD` at all), so this edit
won't show in `git status`/`git diff` — that's expected, not a mistake.
Checked the rest of the file for other stale gortsplib/mediacommon/Go-
version mentions; the remaining ones are version-agnostic, nothing else
to change.

`PLAN.md`: deleted Phase 23's entry entirely, per the decision to revert
both things it documented (the go1.21 pin, and the `gorilla/mux` port it
forced). Reworded Phase 25's "undoing Phase 23's deliberate downgrade"
aside so it doesn't dangle a reference to a now-deleted phase. Reworded
Phase 24's "works uniformly on `mux.Router`" aside for the same reason
(tracing.go's own doc comment, mirrored). Added Phase 26 summarizing
this whole upgrade (issues 01-05 land in this one PLAN.md entry, per
this repo's convention of not restating line-level diffs already in git
history). While in the file, also fixed the "Package/file layout"
table's `web/Dockerfile`/`Dockerfile.farc`+`Dockerfile.hls_server` rows,
which were stale even before this session started (`node:22-alpine`/
`nginx:1.27-alpine`/`golang:1.21-bookworm` vs. the actual
`node:26`/`nginx:alpine` and, after issue 01, `golang:1.26-bookworm`) —
and added the previously-missing `Dockerfile.msm_server` to that same
row. Left Phase 25's stale "confirm GitLab CI's runners" aside alone —
predates this session, unrelated to anything changed here, out of scope.
