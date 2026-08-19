# 05 — web npm deps to latest (incl. majors)

Status: resolved

Not blocked by anything Go-side — can be done independently/in parallel
with 01-04.

## Task

In `web/`, bump every dependency to latest, including majors:

| package | current | target (check for newer at implementation time) |
|---|---|---|
| `react`, `react-dom` | ^18.3.1 | 19.x |
| `react-router-dom` | ^6.28.0 | 7.x |
| `vite` | ^6.0.7 | 8.x |
| `typescript` | ^5.7.2 | 7.x |
| `@vitejs/plugin-react` | ^4.3.4 | 6.x (needs to match Vite 8 peer range) |
| `vitest` | ^4.1.10 | latest |
| `jsdom` | ^30.0.1 | latest |
| `@testing-library/react`, `@testing-library/jest-dom` | 16.3.2 / 7.0.1 | latest |
| `@types/react`, `@types/react-dom` | ^18.3.x | must match React 19 (`^19.x`) |
| `hls.js`, `bootstrap` | 1.5.20 / 5.3.8 | latest patch/minor (no major available) |

## Known considerations (settled during grilling, re-verify against
whatever exact versions land)

- **React Router v7**: `web/src/App.tsx`/`main.tsx` only use
  `<BrowserRouter>`/`<Routes>`/`<Route>` — no `createBrowserRouter`,
  loaders, or actions. v7 keeps this "declarative mode" fully supported
  and `react-router-dom` still re-exports everything needed — no
  framework-mode migration, imports stay as `react-router-dom`. Should
  be close to a drop-in version bump; watch for R19-era ref/type changes
  in `NavLink`'s `isActive` prop typing (`navLinkClass` in `App.tsx`).
- **React 19**: check for removed legacy APIs (`propTypes`,
  `defaultProps` on function components, old `ReactDOM.render` — this
  codebase already uses `createRoot` in `main.tsx` so that part is fine)
  and the new JSX transform's stricter ref handling. `@types/react`/
  `@types/react-dom` MUST be bumped to the matching `19.x` line or
  `tsc -b` will produce spurious type errors.
- **TypeScript 7**: this is the native (Go-based) compiler rewrite —
  behavior should match TS5-era semantics closely, but re-run `tsc -b`
  and read every new diagnostic rather than assuming it's clean; some
  stricter checks reportedly landed alongside the rewrite.
- **Vite 8**: check `web/vite.config.ts`'s plugin config still resolves
  (`@vitejs/plugin-react` major bump needed alongside it — see table
  above) and that the dev proxy config (`vite.config.ts`) still matches
  `web/nginx.conf` — these two have drifted out of sync before (Phase 18
  found a missing `/api/events/` WS proxy route this same way), so
  diff them side by side after the bump regardless of whether Vite
  itself changed proxy behavior.

## Verify

`npm install`, `npm run build` (`tsc -b && vite build`),
`npm test` (`vitest run`). Then manually smoke-test the dev server
(`npm run dev`) against a running farcd+hls_server if not relying solely
on issue 07's Docker e2e pass for player-page regressions.

## Comments

Landed 2026-08-19. All targets: React 19.2.8, `react-router-dom` 7.18.2,
Vite 8.2.1, TypeScript 7.0.2, `@vitejs/plugin-react` 6.0.5,
`@types/react`/`@types/react-dom` bumped to the matching 19.x line,
`hls.js` 1.6.19, everything else at latest patch/minor. Required a full
`rm -rf node_modules package-lock.json && npm install` — reusing the
existing `node_modules` produced `ERESOLVE` warnings and silently kept
React on 18.3.1 despite `package.json` saying `^19.2.8`.

Two real fixes needed, neither in application code:
- `tsconfig.json`: TS7 errors on CSS side-effect imports
  (`main.tsx`'s `bootstrap/dist/css/...` and `./index.css`) without
  `vite/client`'s ambient module declarations — added `"vite/client"` to
  `compilerOptions.types`. This project never had a `vite-env.d.ts`;
  TS5.7 apparently didn't enforce this, TS7 does.
- `PlayerPage.test.tsx`'s gap-skip test failed under React 19. Diagnosed
  by instrumenting the interval callback directly (temporary
  `console.log`s, reverted) rather than guessing: the very first tick
  already computes the correct `{kind:'skip', to:300_000_000n}` — the
  app's `advance()`/interval logic in `playerTimeline.ts` is untouched
  and correct. What's missing is that React 19 commits a state update
  made from a plain `setInterval` callback (outside React's own event
  system) one microtask turn later than before, so the DOM isn't updated
  yet at the exact point the test used to assert. Fixed with one added
  `await vi.advanceTimersByTimeAsync(0)` after the real tick, to flush
  that commit — confirmed via boundary testing (399ms fails, 401ms
  passes before this fix; a zero-length flush after the real 200ms tick
  removes the need for a second real tick entirely) that this is a
  deterministic scheduling change, not flakiness. `PlayerPage.tsx` itself
  has zero diff from HEAD.

Checked `vite.config.ts`'s dev proxy against `web/nginx.conf` as this
ticket asked (they've drifted before — Phase 18). Found `vite.config.ts`
has no `/segments/` proxy entry that `nginx.conf` has (added in Phase
17) — but confirmed via `git diff`/`git log` this predates this session
entirely (`vite.config.ts` has zero diff, last touched by the pre-upgrade
`2e4f4b8` commit) and Vite 8 didn't change proxy config shape (build
succeeded, dev server boots clean under `npm run dev`). Out of scope for
this issue since it's a pre-existing gap unrelated to the version bump —
flagging to the user rather than fixing here.

`npm run build` (`tsc -b && vite build`), `npm test` (`vitest run`, 100/100
passing), `npm run dev` (boots clean), `npm audit` (0 vulnerabilities)
all green.
