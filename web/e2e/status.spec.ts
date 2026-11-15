import { expect, test, type Page } from '@playwright/test'

const status = (freshness: string) => ({ generation: 1, source_time: 1, fetched_at: null, processed_at: null, freshness })
const ops = (page: Page) => page.route('**/api/v1/ops', r => r.fulfill({ json: { status: status('fresh'), jobs: { done: 3, failed: 1 }, outcomes: { published: 2, late: 1 }, schedule: '8f44814c50c2962a' } }))

for (const state of ['fresh', 'stale', 'unavailable']) {
  test(`shows ${state}`, async ({ page }) => {
    await ops(page)
    await page.route('**/api/v1/departures?**', r => r.fulfill({ json: { status: status(state), departures: [] } }))
    await page.goto('/')
    await expect(page.getByRole('status')).toHaveText(state)
  })
}

test('scheduled fallback is labeled', async ({ page }) => {
  await ops(page)
  await page.route('**/api/v1/departures?**', r => r.fulfill({ json: { status: status('stale'), departures: [{ trip: 't1', stop: '1660', time: 1794716880, event_time: null, kind: 'scheduled' }] } }))
  await page.goto('/')
  await expect(page.getByRole('status')).toHaveText('scheduled')
  await expect(page.getByRole('list', { name: 'Departures' })).toContainText('t1')
})

test('outage is explicit', async ({ page }) => {
  await page.route('**/api/v1/ops', r => r.fulfill({ status: 503 }))
  await page.route('**/api/v1/departures?**', r => r.fulfill({ status: 503 }))
  await page.goto('/')
  await expect(page.getByRole('status')).toHaveText('Transit unavailable')
  await expect(page.getByText('Operator summary unavailable')).toBeVisible()
})

test('operator counts render', async ({ page }) => {
  await ops(page)
  await page.route('**/api/v1/departures?**', r => r.fulfill({ json: { status: status('fresh'), departures: [] } }))
  await page.goto('/')
  await expect(page.getByRole('region', { name: 'Jobs' })).toContainText('failed: 1')
  await expect(page.getByRole('region', { name: 'Outcomes' })).toContainText('late: 1')
})
