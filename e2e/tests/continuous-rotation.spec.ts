import { test, expect, type Page } from '@playwright/test'

// Covers .scratch/multi-channel-fcontainer/issues/03-no-fullness-driven-fblock-rotation.md:
// a continuous channel that is NEVER explicitly stopped must still close
// its fblock on its own once full, instead of silently overflowing into
// the next fblock's disk region (reproduced against this exact mediamtx
// stack before the fix -- see that ticket). Deliberately never calls
// recording/stop -- the whole point is that fblock.ready must fire without
// one.
//
// Previously skipped: running this against the real stack's default
// O_DIRECT backend surfaced a deeper, separate format bug --
// .scratch/fblocks-ui/issues/10-header-pad-content-offset-mismatch.md --
// where a fblock completed via the periodic-flush path never actually
// reached Ready (writeTailLocked errored on the tail write). Fixed; this
// test is the e2e verification for that ticket's fix.

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'
const STORAGE_ID = 'rotation-storage'
const CHANNEL = 95

// Small enough that mediamtx/ffmpeg-ch1's real bitrate (~490 KB/s, per
// setup.ts's own measurement of sample.mp4) fills it in well under a
// minute, large enough to stay comfortably above the fchunk_size margin
// internal/storage/segment.go's isFullLocked reserves (see that file --
// without headroom above FCHUNK_SIZE there'd be almost no real capacity
// left to record into).
const FBLOCK_SIZE = 16 * 1024 * 1024
const FCHUNK_SIZE = 4 * 1024 * 1024
const FBLOCK_READY_POLL_TIMEOUT_MS = 90_000
const FBLOCK_READY_POLL_INTERVAL_MS = 2_000

async function ok(res: Response): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ''}`)
  }
  return res
}

async function createStorage(): Promise<void> {
  await ok(
    await fetch(`${FARC_URL}/storages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: STORAGE_ID,
        path: '/data/rotation-storage.bin',
        geometry: { FblockSize: FBLOCK_SIZE, N: 4, MaxChannels: 4 },
        params: {
          fchunk_size: FCHUNK_SIZE,
          write_mode: 'cyclic',
          retention: { days: 1 },
          min_container_share: 0.1,
        },
        force: true,
        catalog_path: '',
        backend: '',
      }),
    }),
  )
}

async function createChannel(): Promise<void> {
  await ok(
    await fetch(`${FARC_URL}/channels`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: CHANNEL,
        rtsp_url: 'rtsp://mediamtx:8554/ch1',
        storage: STORAGE_ID,
        capture_policy: {
          type: 'continuous',
          max_deferred_start_ns: 30_000_000_000,
          prerecord_ns: 0,
          postrecord_ns: 0,
        },
      }),
    }),
  )
}

async function startRecording(): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${CHANNEL}/recording/start`, { method: 'POST' }))
}

type FblockInfo = { index: number; state: string }

async function fblockState(index: number): Promise<string | null> {
  const res = await fetch(`${FARC_URL}/storages/${STORAGE_ID}/fblocks/${index}`)
  if (!res.ok) return null
  const info = (await res.json()) as FblockInfo
  return info.state
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

// waitForFblockReady polls fblock 0's state until it flips to "ready" on
// its own -- recording/stop is never called anywhere in this test, so the
// only way this can happen is internal/storage's own fullness-driven close
// (segmentImpl.isFullLocked/closeForFullnessLocked).
async function waitForFblockReady(index: number): Promise<void> {
  const deadline = Date.now() + FBLOCK_READY_POLL_TIMEOUT_MS
  let lastState: string | null = null
  while (Date.now() < deadline) {
    lastState = await fblockState(index)
    if (lastState === 'ready') return
    if (lastState === 'bad') {
      throw new Error(`fblock ${index} state = bad -- should have closed as Ready on fullness, not failed`)
    }
    await sleep(FBLOCK_READY_POLL_INTERVAL_MS)
  }
  throw new Error(
    `timed out waiting for fblock ${index} to reach ready on its own (last state=${lastState}) -- ` +
      'never-stopped continuous recording should self-close once full (issue 03); check ' +
      'FBLOCK_SIZE/FCHUNK_SIZE in this file are small enough relative to ffmpeg-ch1/sample.mp4\'s bitrate',
  )
}

// A second fblock reaching in_progress/ready proves a fresh segment was
// transparently opened for the still-recording channel after the first
// one closed -- not just that the first one closed and recording silently
// stopped.
async function waitForFblockLive(index: number): Promise<void> {
  const deadline = Date.now() + FBLOCK_READY_POLL_TIMEOUT_MS
  while (Date.now() < deadline) {
    const state = await fblockState(index)
    if (state === 'in_progress' || state === 'ready') return
    await sleep(FBLOCK_READY_POLL_INTERVAL_MS)
  }
  throw new Error(`timed out waiting for fblock ${index} to become live -- continuous recording should have rotated onto it`)
}

async function bumpSearchWindowIntoFuture(page: Page): Promise<void> {
  const toField = page.getByLabel('to', { exact: true })
  const current = await toField.inputValue()
  const d = new Date(current)
  d.setMinutes(d.getMinutes() + 5)
  const pad = (n: number) => String(n).padStart(2, '0')
  await toField.fill(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`)
}

// Migrated to the redesigned multi-channel /player page
// (.scratch/player-redesign/): channel selection is now a checkbox, and
// playback starts from the shared timeline/play button rather than a
// per-candidate "play" link.
async function searchAndPlay(page: Page): Promise<void> {
  await page.goto('/player')
  await page.getByLabel(`channel ${CHANNEL}`, { exact: true }).check()
  await bumpSearchWindowIntoFuture(page)
  await page.getByRole('button', { name: 'Search' }).click()

  await expect(page.getByTestId('player-video-grid')).toBeVisible({ timeout: 30_000 })
  // The search window's default "from" is far earlier than the actual
  // recording -- jump the shared playhead straight to it via "Next" rather
  // than waiting out real wall-clock time for the tick loop to crawl there.
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Play' }).click()

  await expect(async () => {
    const currentTime = await page.locator('video').evaluate((el: HTMLVideoElement) => el.currentTime)
    expect(currentTime).toBeGreaterThan(0)
  }).toPass({ timeout: 30_000 })
  await expect(page.locator('.alert-danger')).toHaveCount(0)
}

async function stopRecording(): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${CHANNEL}/recording/stop`, { method: 'POST' }))
}

async function removeChannel(): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${CHANNEL}`, { method: 'DELETE' }))
}

test.beforeAll(async () => {
  await createStorage()
  await createChannel()
  await startRecording()
})

// The test itself deliberately never calls recording/stop (that's the whole
// point -- fullness-driven rotation with no explicit stop). But leaving
// channel 95 connected and recording forever after the test has already
// finished its own assertions holds up mediamtx's shared ch1 RTSP path for
// every spec file that runs afterward (this suite runs one spec file at a
// time, `playwright.config.ts`'s `workers: 1`) -- confirmed empirically:
// two-channel-playback.spec.ts/player-gap-skip.spec.ts's own real
// recordings started timing out once this channel was left running
// unbounded. Stop and remove it only in afterAll, once every assertion
// above has already run.
test.afterAll(async () => {
  await stopRecording().catch(() => {})
  await removeChannel().catch(() => {})
})

test('never-stopped continuous recording closes its fblock on its own and keeps recording', async ({ page }) => {
  await test.step('fblock 0 reaches ready without recording/stop ever being called', () => waitForFblockReady(0))
  await test.step('a fresh segment opens on fblock 1 for the still-recording channel', () => waitForFblockLive(1))
  await test.step('the content recorded before rotation actually plays back', () => searchAndPlay(page))
})
