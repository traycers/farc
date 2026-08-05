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

async function searchAndPlay(page: Page, channel: number): Promise<void> {
  await page.goto('/player')
  await page.getByLabel('storage').selectOption(STORAGE_ID)
  await page.getByLabel('channel id').fill(String(channel))
  await page.getByRole('button', { name: 'Search' }).click()

  await expect(page.getByRole('button', { name: 'play' }).first()).toBeVisible({ timeout: 30_000 })
  await page.getByRole('button', { name: 'play' }).first().click()

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
