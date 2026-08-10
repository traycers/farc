import { test, expect, type Page } from '@playwright/test'

// Exercises fblock-live/fblock-status against real, decodable RTSP media --
// the one thing internal/api's httptest-backed unit tests and internal/
// farcd's own smoke test (TestRun_ServesFblockTreeAndLiveProgress) can't
// cover: real frames actually flowing from CapturePolicy.HandleFrame into
// the live shared segment, farcd's ~1s ticker picking them up, and a
// genuine fblock.ready producing a TOC the fblock-status page can render.
// Two channels recording into the same storage at once is the central
// claim this round of internal/ingest's rework makes (docs/docs/archive/
// adr/014-channel-registry.md: one fcontainer commonly holds every channel
// of a storage at once) -- CHANNEL/CHANNEL2 both feed this same storage so
// the test can prove they land in one shared fcontainer, not two.

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'
const STORAGE_ID = 'fblock-tree-storage'
const CHANNEL = 95
const CHANNEL2 = 96
// Same ~10s window setup.ts uses for a real recording: comfortably under
// this storage's FblockSize at the sample video's bitrate, long enough for
// several 1s live-progress ticks to land before StopRecording.
const RECORD_WINDOW_MS = 10_000

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
        path: '/data/fblock-tree-storage.bin',
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

// Reuses mediamtx's ch1/ch2 paths, already published by docker-compose.e2e.
// yaml's ffmpeg-ch1/ffmpeg-ch2 loops for two-channel-playback.spec.ts and
// journal.spec.ts (channels 91/92) -- both channels here point at the same
// storage, unlike those specs, since the point is to prove they share one
// fcontainer.
async function createChannel(channel: number, rtspPath: string): Promise<void> {
  await ok(
    await fetch(`${FARC_URL}/channels`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: channel,
        rtsp_url: `rtsp://mediamtx:8554/${rtspPath}`,
        storage: STORAGE_ID,
        capture_policy: { type: 'continuous', max_deferred_start_ns: 30_000_000_000, prerecord_ns: 0, postrecord_ns: 0 },
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

async function removeChannel(channel: number): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${channel}`, { method: 'DELETE' }))
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function treeRow(page: Page, text: string) {
  return page.locator('.tree-row', { hasText: text })
}

test.beforeAll(async () => {
  await createStorage()
  await createChannel(CHANNEL, 'ch1')
  await createChannel(CHANNEL2, 'ch2')
})

test.afterAll(async () => {
  await removeChannel(CHANNEL).catch(() => {})
  await removeChannel(CHANNEL2).catch(() => {})
})

test('fblock-live shows real growing tree nodes, fblock-status renders the finalized tree', async ({ page }) => {
  await page.goto('/fblock-live')
  await page.getByLabel('storage').selectOption(STORAGE_ID)
  await expect(page.getByText('подключено')).toBeVisible({ timeout: 15_000 })

  await startRecording(CHANNEL)

  await test.step('live tree grows from real frames', async () => {
    // Structural nodes (real ones, from the actual RTSP stream's SPS/PPS)
    // arrive on the very first non-empty tick.
    await expect(treeRow(page, `channel: ${CHANNEL}`)).toBeVisible({ timeout: 15_000 })
    // "video" alone (the bare role, no value/size) -- a substring match
    // would also hit "configs(video)"/"config(video): ..."/"data(video)"/
    // "codec(video): ..."/"frames(video) [...]", all of which end in
    // something other than the literal word "video".
    await expect(page.locator('.tree-row', { hasText: /video$/ }).first()).toBeVisible({ timeout: 5_000 })
    await expect(treeRow(page, 'frames(video)').first()).toBeVisible({ timeout: 5_000 })

    // Frame nodes are aggregated into a growing counter, not individual
    // rows -- assert it actually grows across two separate ticks, proving
    // real per-frame data (not just the initial empty container) is
    // reaching the page. The real stream re-announces SPS/PPS periodically
    // (visible directly against the running stack earlier: dozens of
    // "config(video)" versions over one recording), so there can be
    // several "frames(video)" containers on screen at once, one per config
    // version -- sum every one of them rather than assuming a single match.
    async function framesCount(): Promise<number> {
      const texts = await treeRow(page, 'frames(video)').allInnerTexts()
      return texts.reduce((sum, text) => {
        const m = /\[(\d+) кадров\]/.exec(text)
        return sum + (m ? Number(m[1]) : 0)
      }, 0)
    }
    await expect(async () => expect(await framesCount()).toBeGreaterThan(0)).toPass({ timeout: 10_000 })
    const firstCount = await framesCount()
    await expect(async () => expect(await framesCount()).toBeGreaterThan(firstCount)).toPass({ timeout: 10_000 })

    // Regression check for the real bug this page surfaced: the sample
    // stream re-announces byte-identical SPS/PPS before every IDR, which
    // used to open a new config(video) per GOP (internal/ingest/rtsp.go's
    // OnPacketRTP callbacks now bytes.Equal-guard against that) -- exactly
    // one config(video), never more, for the whole recording.
    await expect(treeRow(page, 'config(video)')).toHaveCount(1)
  })

  await test.step('fill bar shows prolog/catalog fixed zones and a growing data zone', async () => {
    const bar = page.locator('.progress[role="progressbar"]')
    await expect(bar).toBeVisible()
    await expect(bar.locator('[title^="prolog:"]')).toBeVisible()
    await expect(bar.locator('[title^="catalog:"]')).toBeVisible()

    // The data/free split uses flex-grow (not width%) so tiny fixed
    // sections (prolog/catalog/toc/epilog) can render at a fixed minimum
    // pixel width -- see FblockFillBar.tsx. Read the resolved style via
    // getComputedStyle rather than regexing the "style" attribute string:
    // browsers commonly collapse flex-grow/flex-shrink/flex-basis into the
    // shorthand "flex: G S B" when reflecting style back to an attribute,
    // so a literal "flex-grow:" substring is not reliably present there.
    async function dataZoneFlexGrow(): Promise<number> {
      return Number(await bar.locator('[title^="данные:"]').evaluate((el) => getComputedStyle(el).flexGrow))
    }
    await expect(async () => expect(await dataZoneFlexGrow()).toBeGreaterThan(0)).toPass({ timeout: 10_000 })

    // toc is an estimate (the real TOC only exists once the fblock is
    // finalized) but its flex-grow weight must still visibly increase as
    // more elements accumulate -- otherwise there's no way to read "how
    // close is this fblock to being done" off the bar (see FblockFillBar.tsx).
    async function tocZoneFlexGrow(): Promise<number> {
      return Number(await bar.locator('[title^="toc"]').evaluate((el) => getComputedStyle(el).flexGrow))
    }
    const firstTocFlexGrow = await tocZoneFlexGrow()
    await expect(async () => expect(await tocZoneFlexGrow()).toBeGreaterThan(firstTocFlexGrow)).toPass({ timeout: 10_000 })
  })

  await test.step('a second channel joins and lands in the SAME shared tree', async () => {
    await startRecording(CHANNEL2)
    await expect(treeRow(page, `channel: ${CHANNEL2}`)).toBeVisible({ timeout: 15_000 })
    // Still one page, one tree -- channel 95's branch didn't get replaced or
    // moved to a separate tree when channel 96 started.
    await expect(treeRow(page, `channel: ${CHANNEL}`)).toBeVisible()
    // One config(video) per channel now, each under its own channel branch --
    // still exactly one per channel (the SPS/PPS-duplication regression check
    // from the single-channel step above still holds per channel).
    await expect(treeRow(page, 'config(video)')).toHaveCount(2)
  })

  await sleep(RECORD_WINDOW_MS)

  // Stop channel 95 first while channel 96 is still recording -- per
  // sharedSegment.detach (internal/ingest/segment.go), the shared fcontainer
  // must NOT flush yet, since channel 96 is still actively writing into it.
  // No "предыдущий fblock" link should appear from this alone.
  await stopRecording(CHANNEL)
  await expect(page.getByRole('link', { name: 'предыдущий fblock →' })).not.toBeVisible({ timeout: 2_000 })

  // Channel 96 is now the last one recording into this storage -- stopping
  // it flushes the shared fcontainer, covering both channels' contributions.
  await stopRecording(CHANNEL2)

  // The link's href is the source of truth for "the fblock this recording
  // just produced" -- it comes from the same real-time fblock.ready WS
  // event (a storage-level event, not scoped to any one channel) that
  // FblockLivePage.tsx's own onEvent handler reacts to. Independently
  // re-deriving "the latest one" via GET .../candidates afterwards is not
  // reliable once this storage's fixed N=4 physical slots have cycled
  // through reuse (its ring can hold older fblocks whose *content*
  // timestamps are earlier than a reused slot's neighbors, so "max end"
  // does not necessarily mean "most recently written").
  const link = page.getByRole('link', { name: 'предыдущий fblock →' })
  await expect(link).toBeVisible({ timeout: 15_000 })
  const href = await link.getAttribute('href')
  if (!href) throw new Error('previous fblock link has no href')
  const uuid = new URL(href, 'http://x').searchParams.get('uuid')
  if (!uuid) throw new Error('previous fblock link has no uuid param')

  await test.step('fblocks list page lists the fblock and opens its own status page', async () => {
    await page.goto(`/fblocks?storage=${STORAGE_ID}`)
    const row = page.locator('tbody tr', { hasText: uuid })
    await expect(row).toBeVisible({ timeout: 15_000 })
    await expect(row.getByText('ready', { exact: true })).toBeVisible()
    await row.getByRole('link', { name: 'Open' }).click()

    await expect(treeRow(page, 'root')).toBeVisible({ timeout: 15_000 })
    const channelsRow = treeRow(page, 'channels').first()
    await expect(channelsRow).toBeVisible()
    await channelsRow.click()
    // Both channels' data landed in this ONE finalized fcontainer -- the
    // real, on-disk proof of the shared-segment behavior exercised live
    // above (docs/docs/archive/adr/014-channel-registry.md).
    await expect(treeRow(page, `channel: ${CHANNEL}`)).toBeVisible({ timeout: 10_000 })
    await expect(treeRow(page, `channel: ${CHANNEL2}`)).toBeVisible({ timeout: 10_000 })
  })
})
