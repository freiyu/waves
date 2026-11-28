import { test, expect, type Page } from '@playwright/test';

// The redesigned pages on a real service: a waiting harness, a fork label, and a graph arrangement that survives a reload.
const OP = process.env.WAVES_OPERATOR_TOKEN ?? '';
const IDS = JSON.parse(process.env.WAVES_IDS ?? '{}') as Record<string, string>;

async function signIn(page: Page) {
  const res = await page.request.post('/api/v1/auth/login-links', { headers: { Authorization: `Bearer ${OP}` } });
  expect(res.status()).toBe(201);
  await page.goto((await res.json()).url);
  await page.getByRole('button', { name: 'Open dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
}

test('a harness that waits for the human is shown on the Overview and the Executions page', async ({ page }) => {
  await signIn(page);
  // v1.2.1: a waiting harness is a card under Live now, with Release on it.
  const live = page.getByRole('region', { name: 'Live now' });
  await expect(live.getByText('Waiting for instruction').first()).toBeVisible();
  await expect(live.getByRole('button', { name: /^Release/ }).first()).toBeVisible();
  await page.goto('/#/executions');
  await expect(page.getByText('Waiting for instruction').first()).toBeVisible();
});

test('the Graph draws the fork, and a moved node is saved in the service and survives a reload', async ({ page }) => {
  await signIn(page);
  const problems: string[] = [];
  page.on('pageerror', (e) => problems.push('script error ' + e.message));
  page.on('console', (m) => { if (m.type() === 'error') problems.push('console ' + m.text()); });
  page.on('response', (r) => { if (r.url().includes('/api/') && r.status() >= 400) problems.push(`${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });
  await page.goto('/#/graph');
  await expect(page.locator('.node').first()).toBeVisible();
  await expect(page.getByText(/^fork of /)).toBeVisible(); // the dashed line between the two frames
  const layout = async () => (await (await page.request.get('/api/v1/graph-layout')).json()) as { nodes: Record<string, { x: number; y: number }> };
  expect(Object.keys((await layout()).nodes)).toHaveLength(0);
  const node = page.locator('.node').first();
  const b = (await node.boundingBox())!;
  await page.mouse.move(b.x + 20, b.y + 14);
  await page.mouse.down();
  await page.mouse.move(b.x + 120, b.y + 74, { steps: 8 });
  await page.mouse.up();
  await expect.poll(async () => Object.keys((await layout()).nodes).length, { timeout: 5000 }).toBeGreaterThan(0);
  const saved = await layout();
  await page.reload();
  await expect(page.locator('.node').first()).toBeVisible();
  await page.waitForTimeout(600);
  expect(await layout()).toEqual(saved); // loading and drawing never rewrites it
  // The fork label can be set and cleared from the project area.
  await page.goto(`/#/projects/${IDS.fork}`);
  await expect(page.getByText(/^Fork of /).first()).toBeVisible();
  await page.getByRole('button', { name: 'Fork', exact: true }).click();
  await page.getByLabel('Original project').selectOption('');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText(/^Fork of /)).toHaveCount(0);
  expect(problems).toEqual([]);
});

test('moving between the panes of a project asks the real service for nothing', async ({ page }) => {
  await signIn(page);
  await page.goto(`/#/projects/${IDS.origin}`);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await page.waitForTimeout(1500); // the area has loaded all its panes
  const calls: string[] = [];
  page.on('request', (r) => { if (r.url().includes('/api/')) calls.push(`${r.method()} ${new URL(r.url()).pathname}`); });
  for (const tab of ['Requirements', 'Plan', 'Review', 'Guard', 'Overview']) {
    await page.getByRole('navigation', { name: 'Project pages' }).getByRole('link', { name: new RegExp('^' + tab) }).click();
    await page.waitForTimeout(500);
  }
  expect(calls).toEqual([]);
});
