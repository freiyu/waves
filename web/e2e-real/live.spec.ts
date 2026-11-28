import { test, expect, type Page } from '@playwright/test';

// A stuck-looking state follows the real one on a real service, and carries no reason from an earlier block.
const OP = process.env.WAVES_OPERATOR_TOKEN ?? '';
const IDS = JSON.parse(process.env.WAVES_IDS ?? '{}') as Record<string, string>;

async function signIn(page: Page) {
  const res = await page.request.post('/api/v1/auth/login-links', { headers: { Authorization: `Bearer ${OP}` } });
  expect(res.status()).toBe(201);
  await page.goto((await res.json()).url);
  await page.getByRole('button', { name: 'Open dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
}

test('the badges follow the service, and a retried task carries no old reason', async ({ page, request }) => {
  const h = { Authorization: `Bearer ${OP}` };
  await signIn(page);
  await page.goto('/#/executions');
  const row = page.getByRole('list', { name: 'Timeline' }).getByRole('listitem').filter({ hasText: IDS.live! });
  await expect(row.locator('.badge').first()).toHaveText(/Running/);

  // The execution is cancelled behind the page's back: the badge changes without a reload.
  expect([202, 204]).toContain((await request.post(`/api/v1/executions/${IDS.live}/cancel`, { headers: h, data: {} })).status());
  await expect(row.locator('.badge').first()).toHaveText(/Cancelled/);

  await page.goto(`/#/projects/${IDS.liveProject}`);
  const task = page.locator('li', { hasText: 'Greeting' }).first();
  await expect(task.locator('.badge')).toHaveText('Blocked');
  await expect(page.getByText(/interrupted|recovery required/i)).toHaveCount(0);

  // Retried: Ready again, with nothing left over from the block.
  expect([202, 204]).toContain((await request.post(`/api/v1/tasks/${IDS.liveTask}/retry`, { headers: h, data: {} })).status());
  await expect(task.locator('.badge')).toHaveText('Ready');
  await expect(page.getByText(/interrupted|recovery required/i)).toHaveCount(0);
});
