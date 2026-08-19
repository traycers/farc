// Configures the e2e stack via farcd's existing HTTP API -- no test-only
// backend code needed, this is exactly what web/src/api/farcd.ts already
// wraps. See PLAN.md Phase 17 / the plan file under .claude/plans for why:
// two channels are created pointing at mediamtx's two concurrently-running
// RTSP loops, so they land in the same/overlapping fblock (ADR-014), which
// is the scenario the fragParsingError bug lives in.
//
// Closing model (.scratch/multi-channel-fcontainer/issues/
// 02-ingest-shared-filler-per-storage.md, settled 2026-08-13): a storage's
// active fblock is written into by a single SHARED Filler across every
// channel assigned to it, and that Filler closes (In Progress -> Ready)
// purely on byte-fullness -- recording/stop only stops one channel from
// contributing further frames, it does NOT close the shared Filler for the
// others, and never did after that refactor. An earlier version of this
// file called recording/stop after a fixed RECORD_WINDOW_MS and waited for
// confirmed candidates, which is exactly the pre-refactor model and never
// actually reaches Ready post-refactor -- see continuous-rotation.spec.ts's
// waitForFblockReady for the only mechanism that does.

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'

export const STORAGE_ID = 'e2e-storage'
export const CHANNEL_1 = 1
export const CHANNEL_2 = 2

// Small enough that both channels' combined real bitrate (~490 KB/s each,
// ~980 KB/s together, per the sample video's measured 3.9 Mbps H.264 +
// 15 kbps AAC track) fills fblock 0 to fullness well within
// FBLOCK_READY_POLL_TIMEOUT_MS -- matching continuous-rotation.spec.ts's
// own geometry/reasoning, since fullness is now the only path to Ready.
const FCHUNK_SIZE = 4 * 1024 * 1024 // 4 MiB -- the documented fchunk floor
const FBLOCK_SIZE = 16 * 1024 * 1024 // 16 MiB -- ~16s combined at ~980 KB/s
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
  const res = await fetch(`${FARC_URL}/storages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id: STORAGE_ID,
      path: '/data/e2e-storage.bin',
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
  })
  await ok(res)
}

async function createChannel(channel: number, rtspPath: string): Promise<void> {
  const res = await fetch(`${FARC_URL}/channels`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id: channel,
      rtsp_url: `rtsp://mediamtx:8554/${rtspPath}`,
      storage: STORAGE_ID,
      capture_policy: {
        type: 'continuous',
        max_deferred_start_ns: 30_000_000_000,
        prerecord_ns: 0,
        postrecord_ns: 0,
      },
    }),
  })
  await ok(res)
}

// A 'continuous' channel does not record automatically on creation -- this
// mirrors the "start recording" button ChannelsIndexPage.tsx renders for
// exactly this policy type (POST /channels/{id}/recording/start).
async function startRecording(channel: number): Promise<void> {
  const res = await fetch(`${FARC_URL}/channels/${channel}/recording/start`, { method: 'POST' })
  await ok(res)
}

async function stopRecording(channel: number): Promise<void> {
  await fetch(`${FARC_URL}/channels/${channel}/recording/stop`, { method: 'POST' }).catch(() => {})
}

async function removeChannel(channel: number): Promise<void> {
  await fetch(`${FARC_URL}/channels/${channel}`, { method: 'DELETE' }).catch(() => {})
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

// waitForFblockReady polls fblock 0's state until both channels' combined
// writes have closed it for fullness -- the only path to Ready post the
// shared-Filler refactor (see this file's header comment). Mirrors
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
      'both channels write into one shared Filler (ADR-014) that only closes on fullness; check ' +
      'FBLOCK_SIZE/FCHUNK_SIZE in this file are small enough relative to ffmpeg-ch1/ffmpeg-ch2/sample.mp4\'s bitrate',
  )
}

export async function setupStack(): Promise<void> {
  await createStorage()
  await createChannel(CHANNEL_1, 'ch1')
  await createChannel(CHANNEL_2, 'ch2')
  await startRecording(CHANNEL_1)
  await startRecording(CHANNEL_2)
  await waitForFblockReady(0)
}

// teardownStack stops and removes both channels -- call from a spec's
// afterAll so they don't keep consuming mediamtx/CPU resources for the
// remainder of a suite run (this repo's specs share one real, finite
// mediamtx/farcd stack -- playwright.config.ts's workers:1).
export async function teardownStack(): Promise<void> {
  await stopRecording(CHANNEL_1)
  await stopRecording(CHANNEL_2)
  await removeChannel(CHANNEL_1)
  await removeChannel(CHANNEL_2)
}
