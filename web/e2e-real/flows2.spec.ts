import { test, expect, type Page } from '@playwright/test';

// Two 1.0.0 flows that the redesign had to keep, on a real service: connecting Claude Code to a project (with the passphrase)
// and writing a backup from Settings.
const PASS = process.env.WAVES_PASS ?? '';
const OP = process.env.WAVES_OPERATOR_TOKEN ?? '';
const IDS = JSON.parse(process.env.WAVES_IDS ?? '{}') as Record<string, string>;

async function signIn(page: Page) {
  const res = await page.request.post('/api/v1/auth/login-links', { headers: { Authorization: `Bearer ${OP}` } });
  expect(res.status()).toBe(201);
  await page.goto((await res.json()).url);
  await page.getByRole('button', { name: 'Open dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
}

test('Claude Code is connected to a project from the dashboard, with the passphrase, and shows its hooks', async ({ page }) => {
  await signIn(page);
  await page.goto(`/#/harnesses/connect/${IDS.connect}`);
  await expect(page.getByText(/Edit to .*settings\.local\.json/).first()).toBeVisible();
  // the plan names the new hooks as well as the guard hook
  await expect(page.getByText(/SessionEnd/).first()).toBeVisible();
  await page.getByRole('button', { name: 'Confirm and connect' }).click();
  const dlg = page.getByRole('dialog');
  await dlg.getByLabel('Approval passphrase').fill(PASS);
  await dlg.getByRole('button', { name: 'Connect' }).last().click();
  await expect(dlg).toBeHidden({ timeout: 15_000 });
  await expect(page).toHaveURL(/#\/harnesses/);
  const g = await (await page.request.get(`/api/v1/projects/${IDS.connect}/guard`)).json();
  expect(g.installed).toBe(true);
  expect(g.waiting_hooks).toBe(true);
  expect(g.drift ?? '').toBe('');
});

test('a backup is written from Settings', async ({ page }) => {
  await signIn(page);
  await page.goto('/#/settings');
  await expect(page.getByText('No backup yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Back up now' }).click();
  await expect(page.getByText(/^Last backup /)).toBeVisible({ timeout: 15_000 });
});
