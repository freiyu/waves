import { test, expect, type Page } from '@playwright/test';

// Values come from the Go harness that started this throwaway instance.
const PASS = process.env.WAVES_PASS ?? '';
const OP = process.env.WAVES_OPERATOR_TOKEN ?? '';
const IDS = JSON.parse(process.env.WAVES_IDS ?? '{}') as Record<string, string>;

// signIn takes the official path: the operator token creates a one-time link
// (what `waves open` prints) and the browser opens it.
async function signIn(page: Page) {
  const res = await page.request.post('/api/v1/auth/login-links', { headers: { Authorization: `Bearer ${OP}` } });
  expect(res.status()).toBe(201);
  const { url } = await res.json();
  await page.goto(url);
  await page.getByRole('button', { name: 'Open dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  return url as string;
}

async function passphrase(page: Page, button: string | RegExp) {
  const dlg = page.getByRole('dialog');
  // After a wrong passphrase the service makes the next try wait (1 s,
  // doubling); a person waits and types again.
  for (let attempt = 0; attempt < 3; attempt++) {
    await expect(dlg.getByLabel('Approval passphrase')).toBeEnabled({ timeout: 10_000 });
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: button }).last().click();
    // Either the dialog closes (done) or the service asked to wait.
    await expect.poll(async () => (await dlg.isHidden()) || (await dlg.getByText(/Try again in/).count()) > 0, { timeout: 10_000 }).toBe(true);
    if (await dlg.isHidden()) return;
    await expect(dlg.getByText(/Try again in/)).toHaveCount(0, { timeout: 10_000 });
  }
  await expect(dlg).toBeHidden();
}

test('a used sign-in link does not work twice', async ({ page, browser }) => {
  const url = await signIn(page);
  const other = await browser.newPage();
  await other.goto(url);
  await expect(other.getByText('This link was already used or has expired.')).toBeVisible();
});

test('needs-attention shows the gate, the blocked task and the interrupted execution', async ({ page }) => {
  await signIn(page);
  const attn = page.getByRole('list', { name: 'Needs attention' });
  await expect(attn).toContainText('Phase Start');
  await expect(attn).toContainText('Interrupted');
  await expect(attn).toContainText('Blocked');
});

test('a phase start gate is approved with the passphrase', async ({ page }) => {
  await signIn(page);
  await page.goto(`/#/approvals/${IDS.gate}`);
  await page.getByRole('button', { name: 'Approve' }).click();
  const dlg = page.getByRole('dialog');
  await dlg.getByLabel('Approval passphrase').fill('wrong passphrase here');
  await dlg.getByRole('button', { name: 'Approve' }).click();
  await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
  await passphrase(page, 'Approve');
  await expect(page.getByText('Decided:')).toBeVisible();
  await expect(page.getByText('approved').first()).toBeVisible();
  const leaked = await page.evaluate((p) => JSON.stringify([localStorage, sessionStorage, location.href, document.cookie]).includes(p), PASS);
  expect(leaked).toBe(false);
});

test('a workflow is rejected with feedback', async ({ page }) => {
  await signIn(page);
  await page.goto(`/#/approvals/${IDS.workflow}`);
  await page.getByRole('button', { name: 'Reject with feedback' }).click();
  const dlg = page.getByRole('dialog');
  await dlg.getByLabel('Feedback (required)').fill('Split the greeting task in two.');
  // Rejecting a workflow is a human decision: it asks for the passphrase.
  await expect(dlg.getByLabel('Approval passphrase')).toBeVisible();
  await passphrase(page, /Reject/);
  await expect(dlg).toBeHidden();
  await expect(page.getByText('Decided:')).toBeVisible();
  await expect(page.getByText('rejected').first()).toBeVisible();
});

test('an interrupted execution is resolved by the human, never on its own', async ({ page }) => {
  await signIn(page);
  await page.goto(`/#/executions/${IDS.interrupted}/recovery`);
  await expect(page.getByRole('heading', { name: /needs your decision/ })).toBeVisible();
  await page.getByRole('button', { name: 'Retry' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Retry task' }).click();
  await expect(page.getByRole('dialog')).toBeHidden();
  await page.goto(`/#/executions/${IDS.interrupted}/recovery`);
  await expect(page.getByText('This execution needs no recovery decision.')).toBeVisible();
});
