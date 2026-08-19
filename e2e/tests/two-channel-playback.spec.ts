import { test, expect, type Page } from '@playwright/test'
import { setupStack, teardownStack, CHANNEL_1, CHANNEL_2 } from './setup'

// Reproduces the reported fragParsingError end to end: two channels record
// concurrently (via setup.ts) into the same/overlapping fblock (ADR-014's
// normal multi-channel scenario), then a real headless Chromium session
// drives the redesigned multi-channel /player page exactly as a user would
// -- both channels checked at once, in a shared 1x2 grid, with one shared
// play button (.scratch/player-redesign/). A fatal hls.js error still
// renders into `.alert-danger` (VideoTile.tsx's setError), so failure here
// reproduces the bug's exact user-visible symptom.
test.beforeAll(async () => {
  await setupStack()
})

test.afterAll(async () => {
  await teardownStack()
})

// PlayerPage.tsx's default "to" is `now`, but <input type="datetime-local">
// only has minute granularity -- re-parsing its displayed value truncates
// seconds to :00, which can retroactively exclude a candidate whose actual
// end falls later within that same minute (exactly what happened against
// this harness's short, just-recorded segments). Bump "to" a few minutes
// into the future -- read from the browser's own field so it shares
// whatever timezone the page itself is rendering in, rather than assuming
// Node's clock/timezone lines up with the browser's.
async function bumpSearchWindowIntoFuture(page: Page): Promise<void> {
  const toField = page.getByLabel('to', { exact: true })
  const current = await toField.inputValue()
  const d = new Date(current)
  d.setMinutes(d.getMinutes() + 5)
  const pad = (n: number) => String(n).padStart(2, '0')
  await toField.fill(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`)
}

test('both channels recorded into a shared fblock play at once in a shared grid', async ({ page }) => {
  await page.goto('/player')

  await page.getByLabel(`channel ${CHANNEL_1}`, { exact: true }).check()
  await page.getByLabel(`channel ${CHANNEL_2}`, { exact: true }).check()
  await bumpSearchWindowIntoFuture(page)
  await page.getByRole('button', { name: 'Search' }).click()

  // 2 checked channels -> the plan's 1x2 grid (spec.md), one tile per
  // channel, both real <video> elements.
  await expect(page.getByTestId('player-video-grid')).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('video')).toHaveCount(2)

  // The search window's default "from" is an hour before the (just-recorded,
  // few-seconds-long) real segment -- jump the shared playhead straight to
  // it via "Next" (the same segment-navigation button a real user would
  // reach for), rather than waiting out real wall-clock time for the tick
  // loop to crawl there on its own.
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Play' }).click()

  // The strong assertion: both tiles' real decoded playback actually
  // progressed at once, not just "no error observed yet".
  await expect(async () => {
    const times = await page.locator('video').evaluateAll((els) => els.map((el) => (el as HTMLVideoElement).currentTime))
    expect(times).toHaveLength(2)
    expect(times[0]).toBeGreaterThan(0)
    expect(times[1]).toBeGreaterThan(0)
  }).toPass({ timeout: 30_000 })

  // Must still hold after the wait above -- a fatal hls.js error can arrive
  // shortly after playback nominally "starts".
  await expect(page.locator('.alert-danger')).toHaveCount(0)
})
