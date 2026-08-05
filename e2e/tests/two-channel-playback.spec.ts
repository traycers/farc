import { test, expect, type Page } from '@playwright/test'
import { setupStack, STORAGE_ID, CHANNEL_1, CHANNEL_2 } from './setup'

// Reproduces the reported fragParsingError end to end: two channels record
// concurrently (via setup.ts) into the same/overlapping fblock -- ADR-014's
// normal multi-channel scenario -- then a real headless Chromium session
// drives the actual PlayerPage UI exactly as a user would. No test-only
// hooks in web/: a fatal hls.js error already renders into `.alert-danger`
// (PlayerPage.tsx's setError, wired up earlier this session), so failure
// here reproduces the bug's exact user-visible message.
test.beforeAll(async () => {
  await setupStack()
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
  // exact: true -- getByLabel's default substring match makes plain 'to'
  // match the "storage" select too ("s-TO-rage").
  const toField = page.getByLabel('to', { exact: true })
  const current = await toField.inputValue()
  const d = new Date(current)
  d.setMinutes(d.getMinutes() + 5)
  const pad = (n: number) => String(n).padStart(2, '0')
  await toField.fill(`${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`)
}

async function searchAndPlay(page: Page, channel: number): Promise<void> {
  await page.goto('/player')
  await page.getByLabel('storage').selectOption(STORAGE_ID)
  await page.getByLabel('channel id').fill(String(channel))
  await bumpSearchWindowIntoFuture(page)
  await page.getByRole('button', { name: 'Search' }).click()

  await expect(page.getByRole('button', { name: 'play' }).first()).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: 'play' }).first().click()

  // PlayerPage.tsx's <video> has neither `autoplay` nor an explicit
  // .play() call -- it only attaches hls.js/sets .src, same as a real
  // browser leaving playback for the native player controls. A real user
  // would press play next; do the same here rather than asserting on a
  // video that was never asked to play.
  // .catch: a rejected play() (e.g. no valid source yet) should surface via
  // the currentTime/.alert-danger assertions below, not as a raw evaluate()
  // exception.
  await page.locator('video').evaluate((el: HTMLVideoElement) => el.play().catch(() => {}))

  // The strong assertion: real decoded playback actually progressed, not
  // just "no error observed yet".
  await expect(async () => {
    const currentTime = await page.locator('video').evaluate((el: HTMLVideoElement) => el.currentTime)
    expect(currentTime).toBeGreaterThan(0)
  }).toPass({ timeout: 30_000 })

  // Must still hold after the wait above -- a fatal hls.js error can arrive
  // shortly after playback nominally "starts".
  await expect(page.locator('.alert-danger')).toHaveCount(0)
}

test('both channels recorded into a shared fblock actually play', async ({ page }) => {
  await test.step(`channel ${CHANNEL_1} plays`, () => searchAndPlay(page, CHANNEL_1))
  await test.step(`channel ${CHANNEL_2} plays`, () => searchAndPlay(page, CHANNEL_2))
})
