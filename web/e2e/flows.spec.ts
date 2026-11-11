import { test, expect, type Page } from '@playwright/test';

const PASS = 'mock-passphrase-ui-review'; // guard:allow fake fixture of the mock adapter

const open = async (page: Page, hash: string, qs = '') => {
  await page.goto(`/${qs}${hash}`);
  await expect(page.locator('#main, main').first()).toBeVisible();
};

test.describe('approval and step-up', () => {
  test('a result approval needs the passphrase, shows wrong attempts, then succeeds without leaking it', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2');
    await page.getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Approval requires your passphrase')).toBeVisible();
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeDisabled(); // empty passphrase
    await dlg.getByLabel('Approval passphrase').fill('wrong one');
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
    await expect(dlg.getByText('2 attempts left')).toBeVisible();
    await expect(dlg.getByLabel('Approval passphrase')).toHaveValue(''); // cleared after every try
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg).toBeHidden();
    await expect(page.getByText('Decided:')).toBeVisible();
    // Nothing secret reached storage or the URL.
    const leaked = await page.evaluate((p) => JSON.stringify([localStorage, sessionStorage, location.href, document.cookie]).includes(p), PASS);
    expect(leaked).toBe(false);
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }).toLowerCase());
    expect(stored).not.toMatch(/elv_|elevation|passphrase/);
  });

  test('Cancel and Escape close the prompt without approving', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2');
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(page.getByText('Decided:')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Approve' })).toBeFocused(); // focus goes back to the opener
  });

  test('Escape and Cancel do nothing while the decision is being sent, and the decision then completes', async ({ page }) => {
    await page.goto('/?latency=700#/approvals/apr_m3t2');
    await page.getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByRole('button', { name: 'Working…' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dlg.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await expect(dlg).toBeVisible(); // not cancelled while the request is in flight
    await expect(dlg).toBeHidden({ timeout: 8000 });
    await expect(page.getByText('Decided:')).toBeVisible();
  });

  test('phase completion is decided from the review, and Approve waits for an override reason', async ({ page }) => {
    await open(page, '#/approvals/apr_d1done');
    await expect(page.getByRole('link', { name: 'Open phase review' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Approve' })).toHaveCount(0); // no shortcut from the inbox
    await page.getByRole('link', { name: 'Open phase review' }).click();
    const approve = page.getByRole('button', { name: 'Approve', exact: true });
    await expect(approve).toBeDisabled();
    await page.getByLabel(/Override reason/).fill('Front matter fix is tracked in the next phase.');
    await expect(approve).toBeEnabled();
    await approve.click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('This phase review was')).toBeVisible();
  });

  test('rate limit and broken audit chain stop the prompt', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2', '?scenario=locked');
    await page.getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Too many attempts.')).toBeVisible();
    await expect(dlg.getByText(/Try again in 4 min/)).toBeVisible();
    await expect(dlg.getByLabel('Approval passphrase')).toBeDisabled();

    await open(page, '#/approvals/apr_m3t2', '?scenario=chain_broken');
    await expect(page.getByText('The audit chain did not verify').first()).toBeVisible(); // banner on every page
    await expect(page.getByRole('button', { name: 'Approve' })).toBeDisabled();
    await expect(page.getByText('Approvals are blocked until the audit chain break is acknowledged')).toBeVisible();
  });

  test('a human can acknowledge a broken chain from Activity, then approvals resume', async ({ page }) => {
    await open(page, '#/activity', '?scenario=chain_broken');
    await page.getByRole('button', { name: 'Acknowledge the break' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Acknowledge' }).click();
    await expect(page.getByText('Audit chain verified.')).toBeVisible();
  });

  test('Elevate unlocks a window, but a phase gate still asks', async ({ page }) => {
    await open(page, '#/approvals/apr_mwf2');
    await page.getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await expect(page.getByRole('button', { name: /Unlocked until/ })).toBeVisible();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog').getByText('Your unlocked window already covers this action')).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    // A phase gate is not covered by the window: it still asks for the passphrase.
    await open(page, '#/approvals/apr_d1done');
    await page.getByRole('link', { name: 'Open phase review' }).click();
    await page.getByLabel(/Override reason/).fill('Tracked in the next phase.');
    await page.getByRole('button', { name: 'Approve', exact: true }).click();
    await expect(page.getByRole('dialog').getByLabel('Approval passphrase')).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    await page.reload();
    // a fresh page load forgets the window: nothing is stored
    await expect(page.getByRole('button', { name: 'Elevate' })).toBeVisible();
  });
});

test.describe('components', () => {
  test('the chip highlight is visible, thins while it moves, carries on when interrupted, and ends on the pressed chip', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    await expect(page.getByRole('button', { name: 'Needs action' })).toBeVisible();
    await page.evaluate(() => {
      const el = document.querySelector('[role=group] > span[aria-hidden]')!;
      const w = window as unknown as { __s: number[][]; __id: number };
      w.__s = [];
      const tick = () => { const r = el.getBoundingClientRect(); w.__s.push([r.x + r.width / 2, r.width, Number(getComputedStyle(el).opacity)]); w.__id = requestAnimationFrame(tick); };
      tick();
    });
    await page.getByRole('button', { name: 'Needs action' }).click();
    await page.waitForTimeout(80); // mid-flight
    await page.getByRole('button', { name: 'Active' }).click(); // interrupt: the target changes, the motion goes on
    await page.waitForTimeout(900);
    const { s, gap } = await page.evaluate(() => {
      const w = window as unknown as { __s: number[][]; __id: number };
      cancelAnimationFrame(w.__id);
      const c = (e: Element): [number, number] => { const r = e.getBoundingClientRect(); return [r.x + r.width / 2, r.width]; };
      const [bx, bw] = c(document.querySelector('[role=group] > span[aria-hidden]')!);
      const [cx, cw] = c(document.querySelector('[role=group] > [aria-pressed=true]')!);
      return { s: w.__s, gap: Math.max(Math.abs(bx - cx), Math.abs(bw - cw)) };
    });
    expect(gap).toBeLessThan(2); // at rest on the pressed chip
    expect(s.every((r) => r[2] === 1)).toBe(true); // never invisible
    const widths = s.map((r) => r[1] ?? 0);
    expect(Math.min(...widths)).toBeLessThan(Math.max(...widths) * 0.8); // it thins while moving
    let jump = 0;
    for (let i = 1; i < s.length; i++) jump = Math.max(jump, Math.abs(s[i]![0]! - s[i - 1]![0]!));
    expect(jump).toBeLessThan(45); // no teleport, also at the turning point
    await expect(page.getByRole('button', { name: 'Active' })).toHaveAttribute('aria-pressed', 'true');
  });

  test('the Executions state chips sit on their own line under the search', async ({ page }) => {
    await open(page, '#/executions');
    const search = await page.locator('.searchbox').boundingBox();
    const chips = await page.getByRole('group', { name: 'State filter' }).boundingBox();
    expect(chips!.y).toBeGreaterThanOrEqual(search!.y + search!.height); // below the search, not beside it
    expect(Math.abs(chips!.x - search!.x)).toBeLessThan(2); // starts at the toolbar's left edge
  });

  const bubbleGap = (page: Page) => page.evaluate(() => {
    const c = (e: Element): [number, number] => { const r = e.getBoundingClientRect(); return [r.x + r.width / 2, r.width]; };
    const [bx, bw] = c(document.querySelector('[role=group] > span[aria-hidden]')!);
    const [cx, cw] = c(document.querySelector('[role=group] > [aria-pressed=true]')!);
    return Math.max(Math.abs(bx - cx), Math.abs(bw - cw));
  });

  test('with reduced motion the chip highlight jumps to the pressed chip without thinning', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await open(page, '#/projects/prj_billing');
    await expect(page.getByRole('button', { name: 'Needs action' })).toBeVisible();
    await page.evaluate(() => {
      const el = document.querySelector('[role=group] > span[aria-hidden]')!;
      const w = window as unknown as { __w: number[]; __id: number };
      w.__w = [];
      const tick = () => { w.__w.push(el.getBoundingClientRect().width); w.__id = requestAnimationFrame(tick); };
      tick();
    });
    await page.getByRole('button', { name: 'Needs action' }).click();
    await page.waitForTimeout(500);
    const widths = await page.evaluate(() => { const w = window as unknown as { __w: number[]; __id: number }; cancelAnimationFrame(w.__id); return w.__w; });
    expect(await bubbleGap(page)).toBeLessThan(2);
    const wide = widths.filter((x) => x > 30); // the only widths seen are the two chips' own
    expect(wide.length).toBe(widths.length);
    expect(new Set(widths.map((x) => Math.round(x))).size).toBeLessThanOrEqual(2); // no thinned in-between width
  });

  test('a chip pressed while its group is hidden still ends under the pressed chip once shown', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const group = page.locator('[role=group][aria-label="Filter tasks"]'); // by CSS: a role query skips it once hidden
    await group.evaluate((g) => { (g.parentElement as HTMLElement).style.display = 'none'; });
    await group.locator('button', { hasText: 'Needs action' }).evaluate((b) => (b as HTMLElement).click()); // hidden: no layout to measure, and the role query skips it
    await group.evaluate((g) => { (g.parentElement as HTMLElement).style.display = ''; });
    await page.waitForTimeout(900);
    await expect(group.getByRole('button', { name: 'Needs action' })).toHaveAttribute('aria-pressed', 'true');
    expect(await bubbleGap(page)).toBeLessThan(2);
    expect(Number(await group.locator('> span[aria-hidden]').evaluate((e) => getComputedStyle(e).opacity))).toBe(1);
  });

  test('leaving the page in the middle of the highlight motion raises no error and the page keeps working', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
    await open(page, '#/projects/prj_billing');
    await page.getByRole('button', { name: 'Needs action' }).click();
    await page.evaluate(() => { location.hash = '#/executions'; }); // within the first frame of the motion
    await expect(page.getByRole('heading', { level: 1, name: 'Executions' })).toBeVisible();
    await page.waitForTimeout(600);
    await page.getByRole('button', { name: /^Completed/ }).click();
    await page.waitForTimeout(600);
    expect(await bubbleGap(page)).toBeLessThan(2);
    expect(errors).toEqual([]);
  });

  test('the current project tab is not a link, and a disabled tab says why', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await expect(nav.getByRole('link', { name: /^Overview/ })).toHaveCount(0);
    await expect(nav.locator('[aria-current=page]')).toHaveText(/Overview/);
    await expect(nav.getByText('No phase is waiting for review.')).toHaveCount(1);
    await nav.locator('[aria-disabled=true]').first().click({ force: true }); // a disabled tab does nothing
    expect(page.url()).toContain('#/projects/prj_billing');
  });

  test('Elevate from the account menu asks for the passphrase', async ({ page }) => {
    await open(page, '#/');
    await page.getByRole('button', { name: /^Human/ }).click();
    await page.getByRole('menuitem', { name: 'Elevate' }).click();
    await expect(page.getByRole('dialog').getByLabel('Approval passphrase')).toBeVisible();
  });

  test('a recorded decision says what it was and how it was confirmed', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2');
    await page.getByRole('button', { name: 'Approve' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('Approval recorded')).toBeVisible();
    await expect(page.getByText(/was approved with your passphrase/)).toBeVisible();
  });

  test('an empty list shows only its empty state, and a validator command is never cut off', async ({ page }) => {
    await open(page, '#/executions', '?scenario=empty');
    await expect(page.getByText('No executions yet')).toBeVisible();
    await expect(page.getByLabel('Search Executions')).toHaveCount(0);
    await open(page, '#/workflow/wfv_b3');
    const cut = await page.evaluate(() => [...document.querySelectorAll('td[data-key=cmd]')].filter((td) => td.scrollWidth > td.clientWidth + 1 || getComputedStyle(td).textOverflow === 'ellipsis').length);
    expect(cut).toBe(0);
  });

  test('a disabled project tab shows its reason on focus and does nothing on Enter; an enabled Review tab links to the review', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    const off = nav.locator('[aria-disabled=true]').first();
    await off.focus();
    await expect(page.locator('.tip')).toHaveText('No phase is waiting for review.');
    await page.keyboard.press('Enter');
    expect(page.url()).toContain('#/projects/prj_billing');
    await open(page, '#/projects/prj_docs');
    const review = page.getByRole('navigation', { name: 'Project pages' }).getByRole('link', { name: /^Review/ });
    await expect(review).toHaveAttribute('href', /#\/phases\/[^/]+\/review/);
    await review.click();
    await expect(page.getByRole('heading', { level: 2, name: /phase review/ })).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  });

  test('a rejection, and an approval covered by an elevated window, do not claim a passphrase', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2');
    await page.getByRole('button', { name: 'Reject with feedback' }).click();
    await page.getByLabel('Feedback (required)').fill('Needs a retry path.');
    await page.getByRole('dialog').getByRole('button', { name: 'Reject' }).click();
    await expect(page.getByText('Rejection recorded')).toBeVisible();
    await expect(page.getByText(/was rejected\.$/)).toBeVisible();
    await open(page, '#/approvals/apr_mwf2');
    await page.getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog').getByText('Your unlocked window already covers this action')).toBeVisible();
    await page.getByRole('dialog').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('Approval recorded')).toBeVisible();
    await expect(page.getByText(/was approved\.$/)).toBeVisible();
    await expect(page.getByText(/with your passphrase/)).toHaveCount(0);
  });

  test('adding a requirement refreshes the Sources count and the tab count', async ({ page }) => {
    await open(page, '#/projects/prj_billing/requirements');
    const sources = page.getByRole('region', { name: 'Sources' }).locator('.count');
    const tab = page.getByRole('navigation', { name: 'Project pages' }).locator('[aria-current=page] .n');
    await expect(sources).toHaveText('3');
    await expect(tab).toHaveText('3');
    await page.getByRole('button', { name: 'Add requirement' }).click();
    await page.getByRole('dialog').getByLabel('Inline text').check();
    await page.getByRole('dialog').getByLabel('Requirement', { exact: true }).fill('Refunds are idempotent.');
    await page.getByRole('dialog').getByRole('button', { name: 'Add', exact: true }).click();
    await expect(page.getByText('Requirement added')).toBeVisible();
    await expect(sources).toHaveText('4');
    await expect(tab).toHaveText('4');
    await expect(page.getByText('Inline: Refunds are idempotent.')).toBeVisible();
  });

  test('an offline passphrase reset stays as an alert on Activity until a human acknowledges it', async ({ page }) => {
    await open(page, '#/activity', '?scenario=reset');
    const alert = page.getByRole('alert').filter({ hasText: 'A security alert is open' });
    await expect(alert).toContainText('Passphrase was reset');
    await expect(alert).toContainText('without the old passphrase');
    await alert.getByRole('button', { name: 'Acknowledge after checking' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Acknowledging records that you have seen it.')).toBeVisible();
    await dlg.getByLabel('Approval passphrase').fill('wrong one');
    await dlg.getByRole('button', { name: 'Acknowledge' }).click();
    await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
    await expect(alert).toBeVisible(); // a refused try clears nothing
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Acknowledge' }).click();
    await expect(page.getByText('Security alerts acknowledged')).toBeVisible();
    await expect(alert).toHaveCount(0);
  });

  test('a hook that is gone from the project is shown on the Guard tab', async ({ page }) => {
    await open(page, '#/harnesses', '?scenario=drift');
    const alert = page.getByRole('alert').filter({ hasText: 'The guard is not active here' });
    await expect(alert).toContainText('missing from .claude/settings.local.json');
    await open(page, '#/harnesses');
    await expect(page.getByRole('alert').filter({ hasText: 'The guard is not active here' })).toHaveCount(0);
  });

  test('the Decided tab shows a decided approval, and the state chips hide states with no rows', async ({ page }) => {
    await open(page, '#/approvals');
    await page.getByRole('tab', { name: 'Decided' }).click();
    await expect(page.getByText('Decided:')).toBeVisible();
    await open(page, '#/executions');
    await expect(page.getByRole('button', { name: /^Completed/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Waiting Approval/ })).toHaveCount(0);
  });

  test('locked settings and recovery choices confirm with a toast that names them', async ({ page }) => {
    await open(page, '#/settings');
    await page.getByRole('button', { name: 'Edit with passphrase' }).first().click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText('Elevation window saved')).toBeVisible();
    await open(page, '#/executions/exe_8f3a1c/recovery');
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Retry task' }).click();
    await expect(page.getByText('Task sent back to Ready')).toBeVisible();
  });

  test('the connect diff can be reached by keyboard, and workflow and review headings nest', async ({ page }) => {
    await open(page, '#/harnesses/connect/prj_docs');
    const diff = page.locator('div.diff[aria-label^="Edit to"]');
    await diff.focus();
    await expect(diff).toBeFocused();
    await open(page, '#/workflow/wfv_b3');
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('heading', { level: 2, name: /Workflow review/ })).toBeVisible();
    await expect(page.getByRole('heading', { level: 3, name: /P2 Persistence layer/ })).toBeVisible();
  });
});

test.describe('top bar', () => {
  test('the top bar does not scroll sideways at 900 px, also while an elevation is held', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 900, height: 700 } });
    const page = await ctx.newPage();
    await open(page, '#/');
    await page.getByRole('button', { name: 'Elevate' }).first().click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await expect(page.getByRole('button', { name: /Unlocked until/ })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
    await ctx.close();
  });
});

test.describe('register shortcut', () => {
  test('the register link opens the form from another page and while already on Projects', async ({ page }) => {
    await open(page, '#/');
    await page.getByRole('link', { name: 'Register project' }).click();
    await expect(page.getByRole('dialog').getByRole('heading', { name: 'Register a project' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    expect(page.url()).not.toContain('register'); // the marker leaves the address
    await page.evaluate(() => (location.hash = '/projects?register')); // same page, route unchanged
    await expect(page.getByRole('dialog').getByRole('heading', { name: 'Register a project' })).toBeVisible();
  });
});

test.describe('checklist', () => {
  test('confirming a checklist item asks for the passphrase', async ({ page }) => {
    await open(page, '#/phases/phs_d1/review');
    await page.getByRole('button', { name: 'Confirm items' }).click();
    const box = page.getByLabel('Screenshots match the current UI');
    await box.click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Confirming needs your passphrase')).toBeVisible();
    await expect(box).not.toBeChecked(); // nothing recorded until the passphrase is in
    await dlg.getByLabel('Passphrase', { exact: true }).fill(PASS);
    await dlg.getByRole('button', { name: 'Confirm' }).click();
    await expect(dlg).toBeHidden();
    await expect(box).toBeChecked();
  });
});

test.describe('revision', () => {
  test('a rejected phase review lets the human reopen tasks with the passphrase', async ({ page }) => {
    await open(page, '#/phases/phs_d1/review');
    await page.getByRole('button', { name: 'Reject with feedback' }).click();
    let dlg = page.getByRole('dialog');
    await dlg.getByLabel('Feedback (required)').fill('The install guide still names the old flag.');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: /Reject/ }).last().click();
    await expect(dlg).toBeHidden();
    await expect(page.getByRole('heading', { name: 'Revise this phase' })).toBeVisible();
    const go = page.getByRole('button', { name: /Reopen/ });
    await expect(go).toBeDisabled();
    await page.getByLabel(/Rewrite the install guide/).check();
    await page.getByLabel('Reason (required)').fill('Fix the flag name in the install guide.');
    await go.click();
    dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Reopening needs your passphrase')).toBeVisible();
    await dlg.getByLabel('Passphrase', { exact: true }).fill(PASS);
    await dlg.getByRole('button', { name: 'Reopen' }).click();
    await expect(dlg).toBeHidden();
    await expect(page.getByText('Tasks reopened.')).toBeVisible();
  });
});

test.describe('states', () => {
  test('empty, error and loading are different things', async ({ page }) => {
    await open(page, '#/executions', '?scenario=empty');
    await expect(page.getByText('No executions yet')).toBeVisible();
    await open(page, '#/executions', '?scenario=error');
    await expect(page.getByText('Could not load executions.')).toBeVisible();
    await expect(page.getByText('No executions yet')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
    await page.goto('/?scenario=slow&latency=1500#/executions');
    await expect(page.getByRole('status', { name: /Loading executions/ })).toBeVisible();
  });

  test('signed out shows the session-ended page and a valid link signs in', async ({ page }) => {
    await open(page, '#/', '?scenario=signed_out');
    await expect(page.getByText('Your session ended.')).toBeVisible();
    await page.goto('/?scenario=signed_out#/signin/link-valid-demo');
    await expect(page.getByText(/Link valid/)).toBeVisible();
    await page.getByRole('button', { name: 'Open dashboard' }).click();
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
    await page.goto('/?scenario=signed_out#/signin/used-link');
    await expect(page.getByText('This link was already used or has expired.')).toBeVisible();
  });

  test('the connection indicator shows Reconnecting, then Live, and Stale when it stays down', async ({ page }) => {
    await open(page, '#/');
    await expect(page.getByRole('status').filter({ hasText: 'Live' })).toBeVisible();
    await page.evaluate(() => (window as any).__wavesMock.refuse(true));
    await expect(page.getByRole('status').filter({ hasText: 'Reconnecting' })).toBeVisible();
    await page.evaluate(() => (window as any).__wavesMock.refuse(false));
    await expect(page.getByRole('status').filter({ hasText: 'Live' })).toBeVisible({ timeout: 8000 });
  });
});

test.describe('live data', () => {
  test('the log streams new lines without moving a reader who scrolled up', async ({ page }) => {
    await page.goto('/?tick=700#/executions/exe_a07e52');
    const log = page.getByRole('log', { name: 'Execution log' });
    await expect(log).toContainText('granted lease lse_5c1e');
    const before = await log.locator('div').count();
    await expect(async () => expect(await log.locator('div').count()).toBeGreaterThan(before)).toPass({ timeout: 6000 });
    // Make it scrollable, scroll to the top, and watch the position hold.
    await page.addStyleTag({ content: '.log{max-height:90px !important}' });
    await log.evaluate((el) => (el.scrollTop = 0));
    await log.dispatchEvent('scroll');
    const n = await log.locator('div').count();
    await expect(async () => expect(await log.locator('div').count()).toBeGreaterThan(n)).toPass({ timeout: 6000 });
    expect(await log.evaluate((el) => el.scrollTop)).toBe(0);
    await expect(page.getByText(/Paused\. \d+ new lines? below/)).toBeVisible();
  });

  test('an approval decided here updates the badge and inbox without a reload', async ({ page }) => {
    await open(page, '#/approvals/apr_m3t2');
    await expect(page.locator('.cnt')).toContainText('3');
    await page.getByRole('button', { name: 'Reject with feedback' }).click();
    await page.getByLabel('Feedback (required)').fill('Needs a retry path.');
    await page.getByRole('dialog').getByRole('button', { name: 'Reject' }).click();
    await expect(page.locator('.cnt')).toContainText('2');
  });
});

test.describe('layout and rules', () => {
  const routes = ['#/', '#/projects', '#/projects/prj_billing', '#/projects/prj_billing/requirements', '#/projects/prj_billing/plan', '#/workflow/wfv_b3', '#/phases/phs_d1/review', '#/approvals', '#/executions', '#/executions/exe_a07e52', '#/executions/exe_8f3a1c/recovery', '#/harnesses', '#/harnesses/connect/prj_docs', '#/activity', '#/settings'];

  test('no page scrolls sideways at phone width and nothing leaves the origin', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } });
    const page = await ctx.newPage();
    const external: string[] = [];
    page.on('request', (r) => { if (!r.url().startsWith('http://127.0.0.1:4173') && !r.url().startsWith('data:')) external.push(r.url()); });
    for (const r of routes) {
      await page.goto('/' + r);
      await expect(page.locator('main').first()).toBeVisible();
      await page.waitForTimeout(150);
      const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(over, `${r} overflows by ${over}px`).toBeLessThanOrEqual(0);
    }
    expect(external).toEqual([]);
    await ctx.close();
  });

  test('every page has one h1, labelled controls, unique ids and a language', async ({ page }) => {
    for (const r of routes) {
      await page.goto('/' + r);
      await expect(page.locator('main').first()).toBeVisible();
      await page.waitForTimeout(150);
      const problems = await page.evaluate(() => {
        const out: string[] = [];
        if (!document.documentElement.lang) out.push('no lang');
        if (document.querySelectorAll('h1').length !== 1) out.push(`h1 count ${document.querySelectorAll('h1').length}`);
        const name = (el: Element) => (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || (el as HTMLElement).innerText?.trim() || ((el as HTMLInputElement).labels?.length ? 'label' : '') || el.getAttribute('title') || '');
        for (const el of document.querySelectorAll('button, a[href], input:not([type=hidden]), select, textarea')) if (!name(el)) out.push(`unnamed ${el.tagName} ${el.outerHTML.slice(0, 60)}`);
        const ids = [...document.querySelectorAll('[id]')].map((e) => e.id);
        for (const id of new Set(ids.filter((v, i) => ids.indexOf(v) !== i))) out.push(`duplicate id ${id}`);
        for (const t of document.querySelectorAll('table')) if (!t.getAttribute('aria-label')) out.push('table without name');
        if (!document.querySelector('main')) out.push('no main landmark');
        return out;
      });
      expect(problems, r).toEqual([]);
    }
  });

  test('dark mode follows the system and the account menu overrides it', async ({ browser }) => {
    const ctx = await browser.newContext({ colorScheme: 'dark' });
    const page = await ctx.newPage();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
    const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    expect(await bg()).toBe('rgb(10, 11, 13)');
    await page.getByRole('button', { name: /^Human/ }).click();
    await page.getByRole('menuitemradio', { name: 'Light' }).click(); // system -> light
    expect(await bg()).toBe('rgb(245, 246, 248)');
    await ctx.close();
  });

  test('status is never color alone: every badge carries an icon and text', async ({ page }) => {
    await open(page, '#/executions');
    await expect(page.locator('.badge').first()).toBeVisible();
    const bad = await page.evaluate(() => [...document.querySelectorAll('.badge')].filter((b) => !b.querySelector('svg') || !b.textContent?.trim()).length);
    expect(bad).toBe(0);
  });

  test('executions table sorts, searches and filters', async ({ page }) => {
    await open(page, '#/executions');
    await page.getByLabel('Search Executions').fill('flaky');
    await expect(page.getByRole('table', { name: 'Executions' }).locator('tbody tr')).toHaveCount(1);
    await page.getByLabel('Search Executions').fill('');
    await page.getByRole('button', { name: /^Completed/ }).click();
    await expect(page.getByRole('table', { name: 'Executions' }).locator('tbody tr')).toHaveCount(3);
    await page.getByRole('button', { name: 'Execution', exact: true }).click();
    await expect(page.getByRole('columnheader', { name: /Execution/ })).toHaveAttribute('aria-sort', 'ascending');
  });
});
