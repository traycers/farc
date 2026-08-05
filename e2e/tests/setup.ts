// Configures the e2e stack via farcd's existing HTTP API -- no test-only
// backend code needed, this is exactly what web/src/api/farcd.ts already
// wraps. See PLAN.md Phase 17 / the plan file under .claude/plans for why:
// two channels are created pointing at mediamtx's two concurrently-running
// RTSP loops, so they land in the same/overlapping fblock (ADR-014), which
// is the scenario the fragParsingError bug lives in.

const FARC_URL = process.env.E2E_FARC_URL ?? 'http://localhost:18081'

export const STORAGE_ID = 'e2e-storage'
export const CHANNEL_1 = 1
export const CHANNEL_2 = 2

// Small, fast-rotating geometry so a fblock actually reaches Ready within
// this test's patience window. These sizes are a starting point, not a
// verified-correct value -- the plan flagged fblock-rotation timing as
// something to tune empirically once this harness is actually run against
// a real fblock writer; adjust FBLOCK_SIZE/FCHUNK_SIZE and
// CANDIDATE_POLL_TIMEOUT_MS below if fblocks never reach Ready in time.
const FCHUNK_SIZE = 4 * 1024 * 1024 // 4 MiB -- the documented fchunk floor
const FBLOCK_SIZE = 32 * 1024 * 1024 // 32 MiB -- 8 fchunks per fblock
const CANDIDATE_POLL_TIMEOUT_MS = 60_000
const CANDIDATE_POLL_INTERVAL_MS = 2_000
// How long to let each channel accumulate real frames in memory before
// StopRecording flushes them -- comfortably under FBLOCK_SIZE at the
// sample video's bitrate (~490 KB/s for the 3.9 Mbps H.264 + 15 kbps AAC
// track ffprobe reported), leaving headroom before the ~65s point where a
// single fcontainer would exceed FBLOCK_SIZE's 32 MiB.
const RECORD_WINDOW_MS = 10_000

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

// CapturePolicy.HandleFrame (internal/ingest/policy.go) only queues frames
// into an in-memory Filler while recording -- for the 'continuous' policy
// there is no time/size-based auto-rotation (Tick is a no-op for it), so
// nothing ever reaches disk until StopRecording explicitly closes the
// segment and hands it to storage.Unit.WriteFcontainer. One WriteFcontainer
// call writes exactly one whole fblock (internal/storage/recorder.go), so
// this must run before RECORD_WINDOW_MS produces more content than
// FBLOCK_SIZE can hold.
async function stopRecording(channel: number): Promise<void> {
  const res = await fetch(`${FARC_URL}/channels/${channel}/recording/stop`, { method: 'POST' })
  await ok(res)
}

type Candidate = { index: number; uuid: string; begin: number; end: number }

async function hasConfirmedCandidate(channel: number): Promise<boolean> {
  const t1 = 0n
  // BigInt, not Number: Date.now() * 1e9 ns overflows Number.MAX_SAFE_INTEGER
  // and would serialize in scientific notation, which farcd's strconv.ParseUint
  // (internal/api/query.go's parseQueryChannelTimeRange) rejects.
  const t2 = BigInt(Date.now()) * 1_000_000n
  const url = `${FARC_URL}/storages/${STORAGE_ID}/candidates?channel=${channel}&t1=${t1}&t2=${t2}&confirm=true`
  const res = await fetch(url)
  if (!res.ok) return false
  const rows = (await res.json()) as Candidate[]
  return rows.length > 0
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

// waitForRecordings polls both channels' confirmed candidates until each has
// at least one, or throws after CANDIDATE_POLL_TIMEOUT_MS -- proving both
// channels genuinely recorded real, TOC-confirmed data before the test
// proceeds to playback (reuses the confirm=true endpoint fixed earlier this
// session specifically to rule out mask false positives, ADR-014).
async function waitForRecordings(): Promise<void> {
  const deadline = Date.now() + CANDIDATE_POLL_TIMEOUT_MS
  let ch1Ready = false
  let ch2Ready = false
  while (Date.now() < deadline) {
    ch1Ready ||= await hasConfirmedCandidate(CHANNEL_1)
    ch2Ready ||= await hasConfirmedCandidate(CHANNEL_2)
    if (ch1Ready && ch2Ready) return
    await sleep(CANDIDATE_POLL_INTERVAL_MS)
  }
  throw new Error(
    `timed out waiting for confirmed candidates (channel 1 ready=${ch1Ready}, channel 2 ready=${ch2Ready}) -- ` +
      'check that ffmpeg-ch1/ffmpeg-ch2 are actually publishing into mediamtx, and that FBLOCK_SIZE/FCHUNK_SIZE ' +
      'in this file are small enough for a fblock to reach Ready within CANDIDATE_POLL_TIMEOUT_MS',
  )
}

export async function setupStack(): Promise<void> {
  await createStorage()
  await createChannel(CHANNEL_1, 'ch1')
  await createChannel(CHANNEL_2, 'ch2')
  await startRecording(CHANNEL_1)
  await startRecording(CHANNEL_2)
  await sleep(RECORD_WINDOW_MS)
  await stopRecording(CHANNEL_1)
  await stopRecording(CHANNEL_2)
  await waitForRecordings()
}
