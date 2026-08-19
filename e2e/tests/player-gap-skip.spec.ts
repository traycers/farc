import { test, expect } from '@playwright/test'

// Real event-policy recording, real >=2s idle gap between two triggered
// bursts, driven through the redesigned /player page (.scratch/
// player-redesign/): the timeline must show two separate segments, and
// pressing play must cross the gap automatically -- no manual "next"
// click, per spec.md's gap-skip rule. This is a confirmatory,
// integration-level check; the gap-skip logic itself is exhaustively
// unit-tested in web/src/pages/playerTimeline.test.ts against synthetic
// data (.scratch/player-redesign/issues/02-player-page-redesign.md).
//
// Closing model (see setup.ts's header comment / .scratch/
// multi-channel-fcontainer/issues/02-ingest-shared-filler-per-storage.md):
// a storage's active fblock closes (In Progress -> Ready) purely on
// byte-fullness -- an event channel's own postrecord-driven auto-stop only
// stops it from contributing further frames, it does NOT close the fblock.
// So FBLOCK_SIZE here is deliberately small enough that BOTH triggered
// bursts' *combined* real content crosses fullness sometime during/after
// the second burst -- landing both bursts (and the gap between them) in
// one fblock's TOC, which is what hls_server's video-presence computation
// needs to see the gap at all.
//
// Own local storage/channel/trigger helpers, not shared with setup.ts or
// journal.spec.ts, matching this repo's existing per-spec convention
// (continuous-rotation.spec.ts, journal.spec.ts each own theirs too).

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'
// hls_server has no published host port in production (internal/hlsapi's
// server is only reachable from other containers on the compose network);
// this test runs inside that network (see e2e/README-less docker-run
// invocation), so the compose service name resolves directly.
const HLS_URL = process.env.E2E_HLS_URL ?? 'http://localhost:18090'
const STORAGE_ID = 'gap-skip-storage'
const CHANNEL = 93

const FCHUNK_SIZE = 4 * 1024 * 1024 // 4 MiB -- the documented fchunk floor
// A first attempt at FBLOCK_SIZE=4 MiB (the fchunk floor) closed for
// fullness during burst 1 alone -- at that scale, an fblock's *fixed*
// per-block overhead (prologue/catalog/header-checksums/epilogue, which
// doesn't shrink with FblockSize) apparently dominates real usable content
// budget, rotating through all 4 fblocks within a few seconds instead of
// the expected ~490 KB/s-driven duration. 12 MiB is far enough from that
// small-block regime to behave like continuous-rotation.spec.ts's proven
// 16 MiB (~600 KB/s effective content rate there).
const FBLOCK_SIZE = 12 * 1024 * 1024
// 15s per burst: ~9 MB alone (safely under FBLOCK_SIZE, won't close after
// burst 1 alone), ~18 MB combined (comfortably over FBLOCK_SIZE, crosses
// fullness during burst 2 -- landing both bursts, and the gap between
// them, in one fblock's TOC).
const POSTRECORD_NS = 15_000_000_000
// Comfortably over hls_server's 2s video-presence gap threshold
// (.scratch/player-redesign/issues/01-hls-server-timeline-endpoint.md).
const GAP_IDLE_MS = 3_500
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
        path: '/data/gap-skip-storage.bin',
        geometry: { FblockSize: FBLOCK_SIZE, N: 4, MaxChannels: 4 },
        params: { fchunk_size: FCHUNK_SIZE, write_mode: 'cyclic', retention: { days: 1 }, min_container_share: 0.1 },
        force: true,
        catalog_path: '',
        backend: '',
      }),
    }),
  )
}

// Points at the same real, continuously-published mediamtx path
// two-channel-playback.spec.ts's ffmpeg-ch1 already feeds -- real,
// decodable content, not a synthetic fixture.
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
          type: 'event',
          max_deferred_start_ns: 30_000_000_000,
          prerecord_ns: 0,
          postrecord_ns: POSTRECORD_NS,
        },
      }),
    }),
  )
}

async function triggerEvent(): Promise<void> {
  await ok(await fetch(`${FARC_URL}/channels/${CHANNEL}/events`, { method: 'POST' }))
}

async function stopRecording(): Promise<void> {
  await fetch(`${FARC_URL}/channels/${CHANNEL}/recording/stop`, { method: 'POST' }).catch(() => {})
}

async function removeChannel(): Promise<void> {
  await fetch(`${FARC_URL}/channels/${CHANNEL}`, { method: 'DELETE' }).catch(() => {})
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

type FblockInfo = { index: number; state: string }

async function fblockState(index: number): Promise<string | null> {
  const res = await fetch(`${FARC_URL}/storages/${STORAGE_ID}/fblocks/${index}`)
  if (!res.ok) return null
  const info = (await res.json()) as FblockInfo
  return info.state
}

// waitForFblockReady polls fblock 0 until it closes for fullness -- the
// only path to Ready (see this file's header comment). Mirrors
// continuous-rotation.spec.ts's helper of the same name/shape.
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
      'check that both triggered bursts together produced enough real content to cross FBLOCK_SIZE',
  )
}

type Segment = { begin: bigint; end: bigint }
type RawSegment = { begin: string; end: string }
type RawChannelTimeline = { channel: number; segments: RawSegment[] }

// Queries hls_server's own new timeline endpoint directly (not farcd's
// coarser per-fcontainer `candidates`, which wouldn't show the internal
// gap at all -- candidates spans a whole fcontainer's first-to-last frame
// range, while the video-presence gap-split this test cares about is TOC-
// internal). BigInt-safe parsing, same reasoning as web/src/api/ns.ts.
async function getTimelineSegments(t1: bigint, t2: bigint): Promise<Segment[]> {
  const url = `${HLS_URL}/timeline?channels=${CHANNEL}&t1=${t1}&t2=${t2}`
  const res = await fetch(url)
  if (!res.ok) return []
  const text = await res.text()
  const quoted = text.replace(/"(begin|end)":(\d+)/g, '"$1":"$2"')
  const raw = JSON.parse(quoted) as RawChannelTimeline[]
  const mine = raw.find((c) => c.channel === CHANNEL)
  if (!mine) return []
  return mine.segments.map((s) => ({ begin: BigInt(s.begin), end: BigInt(s.end) }))
}

test.beforeAll(async () => {
  await createStorage()
  await createChannel()
})

test.afterAll(async () => {
  await stopRecording()
  await removeChannel()
})

test('a >=2s idle gap between two triggered bursts shows as 2+ timeline segments, and play crosses it automatically', async ({
  page,
}) => {
  await triggerEvent()
  // Let the first burst run its full postrecord course (real wall-clock
  // time -- there's no way to detect "burst 1 ended" other than waiting,
  // since fblock 0 is deliberately still below fullness at this point).
  await sleep(POSTRECORD_NS / 1_000_000 + 1_000)
  await sleep(GAP_IDLE_MS)
  await triggerEvent()
  await waitForFblockReady(0)

  // hls_server indexes a fblock asynchronously off farcd's WS push
  // (internal/tocindex.EventSubscriber) -- farcd's own catalog can already
  // report the fblock Ready a moment before hls_server's /timeline
  // reflects it, so poll rather than fetching once.
  const t2 = BigInt(Date.now()) * 1_000_000n
  let segments: Segment[] = []
  const deadline = Date.now() + 15_000
  while (Date.now() < deadline) {
    segments = (await getTimelineSegments(0n, t2)).sort((a, b) => (a.begin < b.begin ? -1 : a.begin > b.begin ? 1 : 0))
    if (segments.length >= 2) break
    await sleep(1_000)
  }
  expect(segments.length).toBeGreaterThanOrEqual(2)
  const second = segments[1]

  await page.goto('/player')
  await page.getByLabel(`channel ${CHANNEL}`, { exact: true }).check()

  // Cover the whole recording comfortably -- <input type="datetime-local">
  // truncates to whole minutes, which can otherwise retroactively exclude a
  // just-recorded segment's actual end within the same minute (same
  // workaround as two-channel-playback.spec.ts).
  const toField = page.getByLabel('to', { exact: true })
  const current = await toField.inputValue()
  const d = new Date(current)
  d.setMinutes(d.getMinutes() + 5)
  const pad = (n: number) => String(n).padStart(2, '0')
  await toField.fill(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`)

  await page.getByRole('button', { name: 'Search' }).click()
  await expect(page.getByTestId('player-timeline-row')).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('.player-timeline-segment').first()).toBeVisible()

  // Jump the shared playhead straight to the first recording (same
  // real-time-vs-search-window reasoning as two-channel-playback.spec.ts),
  // then let play carry it across the gap on its own.
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Play' }).click()

  await expect(async () => {
    const ns = await page.getByTestId('player-current-time').getAttribute('data-playhead-ns')
    expect(ns !== null && BigInt(ns) >= second.begin).toBe(true)
  }).toPass({ timeout: 30_000 })

  await expect(page.locator('.alert-danger')).toHaveCount(0)
})
