import { test, expect, type Page } from '@playwright/test'

// Drives the real /journal WebSocket feed end to end through the actual
// nginx-proxied build (web/nginx.conf's /api/events/ location), not just the
// vite dev proxy -- exactly the gap that would have hidden a broken WS
// upgrade in production. Unlike two-channel-playback.spec.ts, this doesn't
// need mediamtx/ffmpeg: CapturePolicy.StartRecording/StopRecording/Trigger
// (internal/ingest/policy.go) flip `recording` and fire onRecordingChange
// synchronously, with no dependency on frames actually arriving -- only
// fblock.created/fblock.deleted would need a real write cycle, which this
// test doesn't attempt (see JournalPage's own doc note on that being
// best-effort / out of scope without live video).

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'
const STORAGE_ID = 'journal-storage'
const CONTINUOUS_CHANNEL = 91
const EVENT_CHANNEL = 92

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
        path: '/data/journal-storage.bin',
        geometry: { FblockSize: 32 * 1024 * 1024, N: 4, MaxChannels: 4 },
        params: {
          fchunk_size: 4 * 1024 * 1024,
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

// Both test channels point at the same mediamtx path the other spec's
// ffmpeg-ch1 already publishes (docker-compose.e2e.yaml) -- a real,
// reachable RTSP source, though this test's assertions don't depend on any
// frame ever actually arriving.
async function createChannel(channel: number, policyType: 'continuous' | 'event'): Promise<void> {
  await ok(
    await fetch(`${FARC_URL}/channels`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: channel,
        rtsp_url: 'rtsp://mediamtx:8554/ch1',
        storage: STORAGE_ID,
        capture_policy: {
          type: policyType,
          max_deferred_start_ns: 30_000_000_000,
          prerecord_ns: 0,
          postrecord_ns: 5_000_000_000,
        },
      }),
    }),
  )
}

async function startRecording(channel: number): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${channel}/recording/start`, { method: 'POST' }))
}

async function stopRecording(channel: number): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${channel}/recording/stop`, { method: 'POST' }))
}

async function triggerEvent(channel: number): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${channel}/events`, { method: 'POST' }))
}

async function removeChannel(channel: number): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${channel}`, { method: 'DELETE' }))
}

// Row lookup scoped to the "channel" column (JournalPage.tsx's 3rd <td>) --
// matching on the whole row's text would also false-match e.g. a channel id
// that happens to be a substring of a timestamp.
function eventRow(page: Page, name: string, channel: number) {
  return page.locator('tbody tr', { hasText: name }).filter({
    has: page.locator('td:nth-child(3)', { hasText: new RegExp(`^${channel}$`) }),
  })
}

async function expectEvent(page: Page, name: string, channel: number): Promise<void> {
  await expect(eventRow(page, name, channel).first()).toBeVisible({ timeout: 10_000 })
}

test.beforeAll(async () => {
  await createStorage()
})

test.afterAll(async () => {
  await removeChannel(CONTINUOUS_CHANNEL).catch(() => {})
  await removeChannel(EVENT_CHANNEL).catch(() => {})
})

test('live journal feed shows channel/recording/trigger events as they happen', async ({ page }) => {
  await page.goto('/journal')
  await expect(page.getByText('connected')).toBeVisible({ timeout: 15_000 })

  await test.step('channel.created (continuous)', async () => {
    await createChannel(CONTINUOUS_CHANNEL, 'continuous')
    await expectEvent(page, 'channel.created', CONTINUOUS_CHANNEL)
  })

  await test.step('recording command + real state transition, start', async () => {
    await startRecording(CONTINUOUS_CHANNEL)
    await expectEvent(page, 'channel.recording.command.start', CONTINUOUS_CHANNEL)
    await expectEvent(page, 'channel.recording.started', CONTINUOUS_CHANNEL)
  })

  await test.step('recording command + real state transition, stop', async () => {
    await stopRecording(CONTINUOUS_CHANNEL)
    await expectEvent(page, 'channel.recording.command.stop', CONTINUOUS_CHANNEL)
    await expectEvent(page, 'channel.recording.stopped', CONTINUOUS_CHANNEL)
  })

  await test.step('trigger fired opens a fresh segment (event policy)', async () => {
    await createChannel(EVENT_CHANNEL, 'event')
    await expectEvent(page, 'channel.created', EVENT_CHANNEL)
    await triggerEvent(EVENT_CHANNEL)
    await expectEvent(page, 'channel.trigger.fired', EVENT_CHANNEL)
    await expectEvent(page, 'channel.recording.started', EVENT_CHANNEL)
  })

  await test.step('channel.removed', async () => {
    await removeChannel(CONTINUOUS_CHANNEL)
    await expectEvent(page, 'channel.removed', CONTINUOUS_CHANNEL)
  })
})
