import { defineConfig, devices } from '@playwright/test'

// Real-media two-channel playback check (see PLAN.md Phase 17 /
// .claude/plans for context): points at the stack started by
// docker-compose.e2e.yaml, not a Playwright-managed webServer, since the
// stack also needs mediamtx + a running RTSP publish loop before the app
// itself is useful to test against — see tests/setup.ts for the actual
// readiness wait (polling confirmed candidates), which webServer's simple
// HTTP-200 healthcheck can't express.
export default defineConfig({
  testDir: './tests',
  timeout: 120_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  // fullyParallel:false only serializes tests *within* one spec file --
  // Playwright still runs separate spec files concurrently across workers
  // by default. Every spec here shares one real, finite mediamtx/farcd
  // stack (two fixed RTSP paths), so concurrent spec files contend for the
  // same recording pipeline -- confirmed empirically once a 4th real-media
  // spec (player-gap-skip.spec.ts) was added: candidates that reliably
  // confirmed running alone started timing out under 4-way worker
  // parallelism. Force one spec file at a time.
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.E2E_WEB_URL ?? 'http://localhost:18080',
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
