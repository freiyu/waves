import { test, expect, type Page } from '@playwright/test';

const open = async (page: Page, hash: string, qs = '') => {
  await page.goto(`/${qs}${hash}`);
  await expect(page.locator('#main, main').first()).toBeVisible();
};

// A blocked, disconnected or recovering task is shown by its state alone. Why it happened is read in the Activity log.
// The pointer of a new test page rests at its top-left corner, over the rail, which opens under a pointer. After every
// navigation, park it over the page instead.
test.beforeEach(async ({ page }) => {
  const goto = page.goto.bind(page);
  page.goto = async (...args) => {
    const response = await goto(...args);
    await page.mouse.move(640, 700);
    return response;
  };
});

test.describe('no reason next to a stuck state', () => {
  test('badges say Blocked and Disconnected and nothing more', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const blocked = page.locator('.badge', { hasText: 'Blocked' });
    await expect(blocked.first()).toBeVisible();
    for (const t of await blocked.allTextContents()) expect(t.trim()).toBe('Blocked');
    await expect(page.getByText(/recovery required|last execution failed/i)).toHaveCount(0);

    await open(page, '#/executions');
    const disconnected = page.locator('.badge', { hasText: 'Disconnected' });
    await expect(disconnected.first()).toBeVisible();
    for (const t of await disconnected.allTextContents()) expect(t.trim()).toBe('Disconnected');
  });

  test('the overview and the recovery page do not explain', async ({ page }) => {
    await open(page, '#/');
    await expect(page.getByText(/recovery required|released with changes|agent reported a blocker/i)).toHaveCount(0);
    await open(page, '#/executions/exe_8f3a1c/recovery');
    await expect(page.getByRole('heading', { level: 1 })).toContainText('needs your decision');
    await expect(page.getByText(/recovery required|missed heartbeats/i)).toHaveCount(0);
  });

  test('the Activity log is where the reason is read', async ({ page }) => {
    await open(page, '#/activity');
    const table = page.getByRole('table', { name: 'Audit records' });
    await expect(table).toBeVisible();
    await expect(table.getByText('In progress → Blocked · recovery required')).toBeVisible();
    await expect(table.getByText('Running → Disconnected · session left')).toBeVisible();
  });
});

// Approval is once per phase: the review lists every task with what the agent said, and an approval that replaces a workflow
// version says what it cancels before the passphrase is asked for.
test.describe('the end of a phase and a replaced workflow', () => {
  test('the phase review lists every task with its summary', async ({ page }) => {
    await open(page, '#/phases/phs_d1/review');
    await expect(page.getByText('Every task of the phase')).toBeVisible();
    await expect(page.getByText('Fixed 12 broken internal links', { exact: false })).toBeVisible();
  });

  test('approving a replacement workflow shows the work it cancels', async ({ page }) => {
    await open(page, '#/projects/prj_mobile/plan');
    await expect(page.getByText('Work this cancels')).toBeVisible();
    const cancels = page.locator('details', { hasText: 'Work this cancels' });
    await expect(cancels.getByText('m3-t3')).toBeVisible();
    await expect(cancels.getByText('Persist selected method in checkout state')).toBeVisible();
    await expect(cancels.getByText(/exe_a07e52/)).toBeVisible();
    await expect(cancels.getByText(/not rerun automatically/)).toBeVisible();
    await expect(cancels.getByRole('listitem').first()).toHaveText(/^m3-t3 Persist selected method in checkout state · running as exe_a07e52$/); // words apart
  });

  test('a connected lease shows no deadline and no heartbeat', async ({ page }) => {
    await open(page, '#/executions/exe_a07e52');
    await expect(page.getByText('held while the session is connected')).toBeVisible();
    await expect(page.getByText(/every \d+ s/)).toHaveCount(0);
  });
});
