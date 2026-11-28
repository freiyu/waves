import { test, expect, type Page } from '@playwright/test';

const PASS = 'mock-passphrase-ui-review'; // guard:allow fake fixture of the mock adapter

const openTools = (page: Page) => page.getByRole('button', { name: 'Tools', exact: true }).click();

const open = async (page: Page, hash: string, qs = '') => {
  await page.goto(`/${qs}${hash}`);
  await expect(page.locator('#main, main').first()).toBeVisible();
};

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

test.describe('approval and step-up', () => {
  test('a result approval needs the passphrase, shows wrong attempts, then succeeds without leaking it', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Approval requires your passphrase')).toBeVisible();
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeDisabled(); // empty passphrase
    await dlg.getByLabel('Approval passphrase').fill('wrong one');
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
    await expect(dlg.getByText('2 attempts left')).toBeVisible();
    await expect(dlg.getByLabel('Approval passphrase')).toHaveValue(''); // cleared after every try
    const field = dlg.getByLabel('Approval passphrase');
    await expect(field).toHaveAttribute('aria-invalid', 'true'); // red frame, a cross at its right end, the words below it
    await expect(dlg.locator('.pw-x')).toBeVisible();
    expect(await field.evaluate((el) => getComputedStyle(el).borderColor)).not.toBe(await dlg.getByRole('button', { name: 'Cancel' }).evaluate((el) => getComputedStyle(el).borderColor));
    expect((await dlg.locator('.pw-msg').boundingBox())!.y).toBeGreaterThan((await field.boundingBox())!.y + (await field.boundingBox())!.height - 1);
    expect(await dlg.locator('.pw').evaluate((el) => getComputedStyle(el).animationName)).toBe('w-shake');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg).toBeHidden();
    await expect(page.getByText('Approval recorded')).toBeVisible();
    // Nothing secret reached storage or the URL.
    const leaked = await page.evaluate((p) => JSON.stringify([localStorage, sessionStorage, location.href, document.cookie]).includes(p), PASS);
    expect(leaked).toBe(false);
    const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }).toLowerCase());
    expect(stored).not.toMatch(/elv_|elevation|passphrase/);
  });

  test('Cancel and Escape close the prompt without approving', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(page.getByText('Decided:')).toHaveCount(0);
    await expect(page.getByRole('button', { name: /^Approve m3-t2/ })).toBeFocused(); // focus goes back to the opener
  });

  test('Escape and Cancel do nothing while the decision is being sent, and the decision then completes', async ({ page }) => {
    await page.goto('/?latency=700#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByRole('button', { name: 'Working…' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dlg.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await expect(dlg).toBeVisible(); // not cancelled while the request is in flight
    await expect(dlg).toBeHidden({ timeout: 8000 });
    await expect(page.getByText('Approval recorded')).toBeVisible();
  });

  test('phase completion is decided from the review, and Approve waits for an override reason and the passphrase', async ({ page }) => {
    await open(page, '#/projects/prj_docs');
    const subject = page.getByRole('region', { name: 'Pending approvals', exact: true }).getByRole('link', { name: 'P1 Content audit', exact: true }); // the subject is the link to where it is decided
    await expect(subject).toBeVisible();
    await expect(page.getByRole('button', { name: /^Approve/ })).toHaveCount(0); // no shortcut from the project page
    await subject.click();
    const approve = page.getByRole('button', { name: 'Approve phase' });
    await expect(approve).toBeDisabled();
    await page.getByLabel('Approval passphrase').fill(PASS);
    await expect(approve).toBeDisabled(); // the reason is still empty
    await page.getByLabel(/Override reason/).fill('Front matter fix is tracked in the next phase.');
    await expect(approve).toBeEnabled();
    await approve.click();
    await expect(page.getByText('This phase review was')).toBeVisible();
    await expect(page.getByText(/was approved with your passphrase/)).toBeVisible();
    await page.waitForTimeout(1500); // every refresh the decision causes has run
    await expect(page.getByText('This phase review was')).toBeVisible(); // the result stays while the human is on the tab
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await nav.getByRole('link', { name: /^Plan/ }).click();
    await nav.getByRole('link', { name: /^Review/ }).click();
    await expect(page.getByText('Nothing to review.')).toBeVisible();
  });

  test('the review form refuses a wrong passphrase, keeps nothing, and Enter never decides', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    const field = page.getByLabel('Approval passphrase');
    await page.getByLabel(/Override reason/).fill('Known front matter issue.');
    await field.fill(PASS);
    await field.press('Enter');
    await page.waitForTimeout(400);
    await expect(page.getByText('This phase review was')).toHaveCount(0); // pressing Enter is not a decision
    await field.fill('wrong one');
    await page.getByRole('button', { name: 'Approve phase' }).click();
    await expect(page.getByText('Wrong passphrase.')).toBeVisible();
    await expect(page.getByText('2 attempts left')).toBeVisible();
    await expect(field).toHaveValue('');
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(page.getByRole('button', { name: 'Approve phase' })).toBeDisabled();
    await expect(page.getByText('This phase review was')).toHaveCount(0);
    await field.fill(PASS);
    await page.getByRole('button', { name: 'Approve phase' }).click();
    await expect(page.getByText('This phase review was')).toBeVisible();
    const stored = await page.evaluate((p) => JSON.stringify([localStorage, sessionStorage, location.href, document.cookie]).includes(p), PASS);
    expect(stored).toBe(false);
  });

  test('a review that changed under the human is not decided: it says so, reloads, and asks for everything again', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review', '?scenario=stale');
    await page.getByLabel(/Override reason/).fill('Known front matter issue.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Approve phase' }).click();
    await expect(page.getByText('The review changed after you opened it.')).toBeVisible();
    await expect(page.getByText('This phase review was')).toHaveCount(0);
    await expect(page.getByLabel('Approval passphrase')).toHaveValue('');
    await expect(page.getByLabel(/Override reason/)).toHaveValue(''); // written about the earlier content
  });

  test('rejecting a phase needs a note and the passphrase', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    const reject = page.getByRole('button', { name: 'Reject', exact: true });
    await expect(reject).toBeDisabled();
    await page.getByLabel(/Note for the agent/).fill('The build still fails.');
    await expect(reject).toBeDisabled(); // no passphrase yet
    await page.getByLabel('Approval passphrase').fill(PASS);
    await expect(reject).toBeEnabled();
  });

  test('a passphrase typed on the review is thrown away when the human leaves the pane', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    await page.getByLabel('Approval passphrase').fill(PASS);
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await nav.getByRole('link', { name: /^Plan/ }).click();
    await nav.getByRole('link', { name: /^Review/ }).click();
    await expect(page.getByLabel('Approval passphrase')).toHaveValue('');
  });

  test('a broken audit chain and a delay stop the review form', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review', '?scenario=chain_broken');
    await expect(page.getByLabel('Approval passphrase')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Approve phase' })).toBeDisabled();
    await open(page, '#/projects/prj_docs/review', '?scenario=locked');
    await page.getByLabel(/Override reason/).fill('Known issue.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Approve phase' }).click();
    await expect(page.getByText(/Try again in 4 min/)).toBeVisible();
    await expect(page.getByLabel('Approval passphrase')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Approve phase' })).toBeDisabled();
  });

  test('rate limit and broken audit chain stop the prompt', async ({ page }) => {
    await open(page, '#/projects/prj_mobile', '?scenario=locked');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Too many attempts.')).toBeVisible();
    await expect(dlg.getByText(/Try again in 4 min/)).toBeVisible();
    await expect(dlg.getByLabel('Approval passphrase')).toBeDisabled();

    await open(page, '#/projects/prj_mobile', '?scenario=chain_broken');
    await expect(page.getByText('The audit chain did not verify').first()).toBeVisible(); // banner on every page
    await expect(page.getByRole('button', { name: /^Approve m3-t2/ })).toBeDisabled();
    await expect(page.getByRole('button', { name: /^Approve Workflow/ })).toBeDisabled();
    await open(page, '#/projects/prj_mobile/plan', '?scenario=chain_broken');
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
    await open(page, '#/projects/prj_mobile/plan');
    await page.getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await expect(page.getByRole('button', { name: /Unlocked until/ })).toBeVisible();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog').getByText('Your unlocked window already covers this action')).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    // A phase gate is not covered by the window: it still asks for the passphrase.
    await open(page, '#/projects/prj_docs/review');
    await page.getByLabel(/Override reason/).fill('Tracked in the next phase.');
    await expect(page.getByLabel('Approval passphrase')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Approve phase' })).toBeDisabled(); // the window does not cover a gate
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

  test('the timeline moves on with the clock: a running bar is longer when the list is read again', async ({ page }) => {
    await page.clock.install();
    await page.goto('/#/executions');
    await page.clock.runFor(1000);
    const bar = page.getByRole('list', { name: 'Timeline' }).getByRole('listitem').filter({ hasText: 'exe_a07e52' }).locator('.tl-bar');
    await expect(bar).toBeVisible();
    const width = () => bar.evaluate((e) => (e as HTMLElement).style.width);
    const before = await width();
    await page.clock.fastForward('10:00');
    await page.getByRole('button', { name: /^Release Refund validation rules/ }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Release' }).click();
    await page.clock.runFor(1500); // the change event, the coalescing delay, the new read
    await expect.poll(width).not.toBe(before);
  });

  test('a failed execution offers what really exists: opening it', async ({ page }) => {
    await open(page, '#/executions');
    const card = page.getByRole('list', { name: 'Needs attention' }).getByRole('listitem').filter({ hasText: 'Fix flaky checkout test' });
    await expect(card).toContainText('waves task retry');
    await expect(card.getByRole('link', { name: /^Open execution/ })).toHaveAttribute('href', /#\/executions\/exe_c41d09$/);
    await open(page, '#/');
    await expect(page.getByRole('list', { name: 'Also needs a look' }).getByRole('link', { name: /Fix flaky checkout test/ })).toBeVisible();
  });

  test('the older executions are an ordinary table: its cells sit side by side', async ({ page }) => {
    await open(page, '#/executions');
    const cells = page.getByRole('table', { name: 'Executions' }).locator('tbody tr').first().locator('td');
    const [a, b] = [(await cells.nth(0).boundingBox())!, (await cells.nth(1).boundingBox())!];
    expect(Math.round(a.y)).toBe(Math.round(b.y));
    expect(b.x).toBeGreaterThanOrEqual(a.x + a.width - 1);
    expect(a.height).toBeLessThan(120);
  });

  test('one row of state chips filters the timeline and the older list together', async ({ page }) => {
    await open(page, '#/executions');
    const line = page.getByRole('list', { name: 'Timeline' }).getByRole('listitem');
    const older = page.getByRole('table', { name: 'Executions' }).locator('tbody tr');
    await expect(line.first()).toBeVisible();
    const [a, b] = [await line.count(), await older.count()];
    const all = await page.getByRole('button', { name: /^All/ }).innerText();
    expect(a + b).toBe(Number(all.match(/\d+/)![0])); // every execution is in exactly one of the two
    await page.getByRole('button', { name: /^Running/ }).click();
    await expect(line).toHaveCount(2);
    await expect(line.first()).toContainText('Running');
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

  test('the current project tab is not a link; every other tab is, and Review says when there is nothing to review', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await expect(nav.getByRole('link', { name: /^Overview/ })).toHaveCount(0);
    await expect(nav.locator('[aria-current=page]')).toHaveText(/Overview/);
    await expect(nav.locator('[aria-disabled=true]')).toHaveCount(0); // no tab is disabled any more: the pane explains itself
    await nav.getByRole('link', { name: /^Review/ }).click();
    expect(page.url()).toContain('#/projects/prj_billing/review');
    await expect(nav.locator('[aria-current=page]')).toHaveText(/Review/);
    await expect(page.getByText('Nothing to review.')).toBeVisible();
  });

  test('Elevate from the account menu asks for the passphrase', async ({ page }) => {
    await open(page, '#/');
    await page.getByRole('button', { name: /^Operator/ }).click();
    await page.getByRole('menuitem', { name: 'Elevate' }).click();
    await expect(page.getByRole('dialog').getByLabel('Approval passphrase')).toBeVisible();
  });

  test('a recorded decision says what it was and how it was confirmed', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('Approval recorded')).toBeVisible();
    await expect(page.getByText(/was approved with your passphrase/)).toBeVisible();
  });

  test('an empty list shows only its empty state, and a validator command is never cut off', async ({ page }) => {
    await open(page, '#/executions', '?scenario=empty');
    await expect(page.getByText('No executions yet')).toBeVisible();
    await expect(page.getByLabel('Search Executions')).toHaveCount(0);
    await open(page, '#/projects/prj_billing/plan');
    const cut = await page.evaluate(() => [...document.querySelectorAll('td[data-key=cmd]')].filter((td) => td.scrollWidth > td.clientWidth + 1 || getComputedStyle(td).textOverflow === 'ellipsis').length);
    expect(cut).toBe(0);
  });

  test('the Review tab of a project with a phase to review shows that review, under one h1', async ({ page }) => {
    await open(page, '#/projects/prj_docs');
    const review = page.getByRole('navigation', { name: 'Project pages' }).getByRole('link', { name: /^Review/ });
    await expect(review).toHaveAttribute('href', /#\/projects\/prj_docs\/review/);
    await review.click();
    await expect(page.getByRole('heading', { level: 2, name: /phase review/ })).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  });

  test('the panes of a project stay mounted: a filter chosen on Overview is still chosen after a visit to Plan', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const chip = page.getByLabel('Overview', { exact: true }).getByRole('button', { name: 'Active' });
    await chip.click();
    await expect(chip).toHaveAttribute('aria-pressed', 'true');
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await nav.getByRole('link', { name: /^Plan/ }).click();
    await expect(nav.locator('[aria-current=page]')).toHaveText(/Plan/);
    await nav.getByRole('link', { name: /^Overview/ }).click();
    await expect(chip).toHaveAttribute('aria-pressed', 'true'); // the pane was never rebuilt, so the choice is still there
  });

  test('a hidden pane is not reachable: it is inert and hidden from the accessibility tree', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const hidden = page.locator('section.pane[aria-hidden=true]');
    await expect(hidden).toHaveCount(4);
    for (const el of await hidden.all()) await expect(el).toHaveJSProperty('inert', true);
  });

  test('a rejection, and an approval covered by an elevated window, do not claim a passphrase', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Reject m3-t2/ }).click();
    await page.getByLabel('Feedback (required)').fill('Needs a retry path.');
    await page.getByRole('dialog').getByRole('button', { name: 'Reject' }).click();
    await expect(page.getByText('Rejection recorded')).toBeVisible();
    await expect(page.getByText(/was rejected\.$/)).toBeVisible();
    await open(page, '#/projects/prj_mobile/plan');
    await page.getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog').getByText('Your unlocked window already covers this action')).toBeVisible();
    await page.getByRole('dialog').getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('Approval recorded')).toBeVisible();
    await expect(page.getByText(/was approved\.$/)).toBeVisible();
    await expect(page.getByRole('region', { name: /Notifications/ }).getByText(/with your passphrase/)).toHaveCount(0); // the toast only; the Guard pane's own text is not the decision
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
    // The Overview pane lists the same sources and is mounted too, so the new line is checked inside the Requirements pane.
    await expect(page.locator('section.pane[aria-label="Requirements"]').getByText('Inline: Refunds are idempotent.').first()).toBeVisible();
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

  // The approval inbox page was removed by the redesign (decisions live in Projects, the Graph and the Overview), so its Decided tab test went with it.
  test('the state chips hide states with no rows', async ({ page }) => {
    await open(page, '#/executions');
    await expect(page.getByRole('button', { name: /^Completed/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Needs decision/ })).toHaveCount(0);
  });

  test('the old approval inbox and workflow page addresses send the human to the Overview', async ({ page }) => {
    for (const hash of ['#/approvals', '#/approvals/apr_m3t2', '#/workflow/wfv_b3']) {
      await open(page, hash);
      await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
      expect(page.url()).toMatch(/#\/$/);
    }
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
    await open(page, '#/projects/prj_billing/plan');
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('heading', { level: 2, name: /Workflow · version 3/ })).toBeVisible();
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
    await page.getByLabel(/Note for the agent/).fill('The install guide still names the old flag.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Reject', exact: true }).click();
    let dlg;
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

  test('an approval decided here updates the attention badge and the decision count without a reload', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    const badge = page.getByRole('link', { name: /need attention/ });
    const n = Number((await badge.innerText()).match(/\d+/)![0]);
    await page.getByRole('button', { name: /^Reject m3-t2/ }).click();
    await page.getByLabel('Feedback (required)').fill('Needs a retry path.');
    await page.getByRole('dialog').getByRole('button', { name: 'Reject' }).click();
    await expect(badge).toContainText(String(n - 1));
  });

  test('Approve and Reject on the Overview open the same passphrase confirmation, and the decision updates the list', async ({ page }) => {
    await open(page, '#/');
    const list = page.getByRole('list', { name: 'Needs your decision' });
    await expect(list.getByRole('listitem')).toHaveCount(3);
    await list.getByRole('listitem').filter({ hasText: 'Wire Apple Pay' }).getByRole('button', { name: 'Reject' }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Feedback (required)').fill('Needs a retry path.');
    await dlg.getByRole('button', { name: 'Reject' }).click();
    await expect(dlg).toBeHidden();
    await expect(list.getByRole('listitem')).toHaveCount(2);
  });
});

test.describe('layout and rules', () => {
  const routes = ['#/', '#/graph', '#/projects/prj_billing/guard', '#/projects/prj_docs/review', '#/projects/prj_mobile', '#/projects', '#/projects/prj_billing', '#/projects/prj_billing/requirements', '#/projects/prj_billing/plan', '#/workflow/wfv_b3', '#/phases/phs_d1/review', '#/approvals', '#/executions', '#/executions/exe_a07e52', '#/executions/exe_8f3a1c/recovery', '#/harnesses', '#/harnesses/connect/prj_docs', '#/activity', '#/settings'];

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
        for (const el of document.querySelectorAll('button, a[href], input:not([type=hidden]), select, textarea')) if (!el.closest('[inert]') && !name(el)) out.push(`unnamed ${el.tagName} ${el.outerHTML.slice(0, 60)}`);
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
    expect(await bg()).toBe('rgb(24, 22, 22)');
    await page.getByRole('button', { name: /^Operator/ }).click();
    await page.getByRole('menuitemradio', { name: 'Light' }).click(); // system -> light, with a cross-fade
    await expect.poll(bg).toBe('rgb(232, 238, 235)');
    await ctx.close();
  });

  test('status is never color alone: every badge carries an icon and text', async ({ page }) => {
    await open(page, '#/executions');
    await expect(page.locator('.badge').first()).toBeVisible();
    const bad = await page.evaluate(() => [...document.querySelectorAll('.badge')].filter((b) => !b.querySelector('svg') || !b.textContent?.trim()).length);
    expect(bad).toBe(0);
  });

  test('the older executions sort and search, and the state chips filter them', async ({ page }) => {
    await open(page, '#/executions');
    const rows = page.getByRole('table', { name: 'Executions' }).locator('tbody tr');
    await page.getByLabel('Search Executions').fill('decimal money');
    await expect(rows).toHaveCount(1);
    await page.getByLabel('Search Executions').fill('');
    await page.getByRole('button', { name: /^Completed/ }).click();
    await expect(rows.filter({ hasText: 'Completed' })).toHaveCount(await rows.count());
    await expect(page.getByRole('list', { name: 'Timeline' }).getByRole('listitem').filter({ hasText: 'Running' })).toHaveCount(0);
    await page.getByRole('button', { name: 'Execution', exact: true }).click();
    await expect(page.getByRole('columnheader', { name: /Execution/ })).toHaveAttribute('aria-sort', 'ascending');
  });
});

test.describe('graph and icon actions', () => {
  test('approving a phase from the Graph needs the override reason and the passphrase, and nothing is decided before the dialog', async ({ page }) => {
    await open(page, '#/graph');
    await page.getByRole('group', { name: /^P1 Content audit/ }).focus(); // focusing brings an off-screen node into view
    await page.getByRole('group', { name: /^P1 Content audit/ }).click();
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toBeVisible();
    await expect(page.getByText('Awaiting phase review').first()).toBeVisible(); // still undecided
    await page.getByRole('complementary', { name: 'Phase detail' }).getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Approval requires your passphrase')).toBeVisible();
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeDisabled(); // no passphrase, no reason yet
    await dlg.getByLabel(/Override reason/).fill('The build failure is a known front matter issue.');
    await dlg.getByLabel('Approval passphrase').fill('not the passphrase');
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
    await expect(page.getByText('Approval recorded')).toHaveCount(0);
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg).toBeHidden();
    await expect(page.getByText('Approval recorded')).toBeVisible();
  });

  test('Escape closes the Graph confirmation without approving', async ({ page }) => {
    await open(page, '#/graph');
    await page.getByRole('group', { name: /^P1 Content audit/ }).focus(); // focusing brings an off-screen node into view
    await page.getByRole('group', { name: /^P1 Content audit/ }).click();
    await page.getByRole('complementary', { name: 'Phase detail' }).getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(page.getByText('Approval recorded')).toHaveCount(0);
    await expect(page.getByRole('complementary', { name: 'Phase detail' }).getByRole('button', { name: 'Approve' })).toBeVisible(); // still pending
  });

  test('approving from the Overview icon asks for the passphrase too', async ({ page }) => {
    await open(page, '#/');
    const row = page.getByRole('list', { name: 'Needs your decision' }).getByRole('listitem').filter({ hasText: 'Wire Apple Pay' });
    await row.getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Approval requires your passphrase')).toBeVisible();
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(row).toBeVisible(); // nothing was decided
  });

  test('a graph node works from the keyboard: Space opens its detail, Enter expands, arrows move it, Escape closes the detail', async ({ page }) => {
    await open(page, '#/graph');
    const node = page.getByRole('group', { name: /^P2 Persistence layer/ });
    await node.focus();
    const left = () => node.evaluate((el) => parseFloat((el as HTMLElement).style.left));
    const before = await left();
    await page.keyboard.press('ArrowRight');
    expect(await left()).toBe(before + 8);
    await page.keyboard.press('Shift+ArrowLeft');
    expect(await left()).toBe(before + 8 - 32);
    const chev = node.getByRole('button', { name: /Collapse|Expand/ });
    const open0 = await chev.getAttribute('aria-expanded');
    await page.keyboard.press('Enter');
    await expect(chev).toHaveAttribute('aria-expanded', open0 === 'true' ? 'false' : 'true');
    await page.keyboard.press(' ');
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toHaveCount(0);
  });

  test('dragging a node moves its saved position, which survives leaving the page, and never changes the workflow', async ({ page }) => {
    await open(page, '#/graph');
    const node = () => page.getByRole('group', { name: /^P3 Payments API/ });
    const left = () => node().evaluate((el) => parseFloat((el as HTMLElement).style.left));
    const before = await left();
    await node().focus();
    const box = await node().boundingBox();
    await page.mouse.move(box!.x + 60, box!.y + 12);
    await page.mouse.down();
    await page.mouse.move(box!.x + 160, box!.y + 52, { steps: 6 });
    await page.mouse.up();
    const moved = await left();
    expect(moved).toBeGreaterThan(before + 40);
    await page.evaluate(() => (location.hash = '/projects')); // leave at once: the pending save is flushed
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await page.evaluate(() => (location.hash = '/graph'));
    await expect(node()).toBeVisible();
    expect(await left()).toBe(moved); // read back from the saved positions
    await expect(node().getByText('Planned')).toBeVisible(); // its state did not change
  });

  test('dragging, nudging and expanding nodes asks the service for nothing, also while a phase review is open in the pane', async ({ page }) => {
    await page.goto('/?tick=0#/graph');
    await page.getByRole('group', { name: /^P1 Content audit/ }).focus(); // focusing brings an off-screen node into view
    await page.getByRole('group', { name: /^P1 Content audit/ }).click();
    await expect(page.getByRole('complementary', { name: 'Phase detail' }).getByText('Requested by')).toBeVisible();
    await page.waitForTimeout(600);
    const calls = () => page.evaluate(() => (window as unknown as { __wavesMock: { calls(): number } }).__wavesMock.calls());
    const before = await calls();
    const node = page.getByRole('group', { name: /^P3 Payments API/ });
    await node.focus();
    await page.waitForTimeout(350); // the canvas pans the focused phase into view: measure once it rests
    const box = await node.boundingBox();
    await page.mouse.move(box!.x + 60, box!.y + 12);
    await page.mouse.down();
    await page.mouse.move(box!.x + 120, box!.y + 40, { steps: 8 });
    await page.mouse.up();
    await node.focus();
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('ArrowDown');
    await page.getByRole('group', { name: /^P2 Persistence layer/ }).focus();
    await page.waitForTimeout(350);
    await page.keyboard.press('Enter'); // expand or collapse from the keyboard
    await page.waitForTimeout(700);
    // only the position save (a PUT to the mock) may have happened: no read of the review or the approval
    expect((await calls()) - before).toBeLessThanOrEqual(1);
  });

  test('the Table view lists the same phases and links to the review', async ({ page }) => {
    await open(page, '#/graph');
    await page.getByRole('button', { name: 'Table', exact: true }).click();
    const list = page.getByRole('list', { name: 'Phases' });
    await expect(list.getByRole('listitem').filter({ hasText: 'P1 · Content audit' })).toBeVisible();
    await expect(list.getByRole('link', { name: 'Approve' })).toHaveAttribute('href', /#\/projects\/prj_docs\/review/);
  });

  test('a project switched off with its chip leaves the graph; a fork sits under the project it came from', async ({ page }) => {
    await open(page, '#/graph');
    const tags = page.locator('.gtag b');
    await expect(tags.first()).toBeVisible();
    const names = await tags.allInnerTexts();
    expect(names.indexOf('billing-api-refund')).toBe(names.indexOf('billing-api') + 1);
    await page.getByRole('button', { name: 'docs-site' }).click();
    await expect(page.getByRole('button', { name: 'docs-site' })).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('.gtag b', { hasText: 'docs-site' })).toHaveCount(0);
  });
});

test.describe('motion', () => {
  // The icon-only actions (and their roll-out) were replaced by labelled buttons on decision cards in v1.2.1.
  test('buttons give under the press, and links draw their underline on hover', async ({ page }) => {
    await open(page, '#/');
    await page.waitForTimeout(700);
    const link = page.locator('a.tlink').first();
    await link.hover();
    await page.waitForTimeout(350);
    expect(await link.evaluate((el) => getComputedStyle(el).backgroundSize)).toMatch(/100%/);
    const btn = page.getByRole('button', { name: 'Elevate' });
    const box = (await btn.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.waitForTimeout(250);
    expect(await btn.evaluate((el) => getComputedStyle(el).transform)).toMatch(/matrix\(0\.97/);
    await page.mouse.move(2, 2); // leave before releasing: no click
    await page.mouse.up();
  });

  test('with reduced motion nothing animates or transitions', async ({ browser }) => {
    const ctx = await browser.newContext({ reducedMotion: 'reduce' });
    const page = await ctx.newPage();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
    const probe = await page.evaluate(() => {
      const all = [...document.querySelectorAll('*')];
      const moving = all.filter((el) => {
        const cs = getComputedStyle(el);
        return (cs.animationName !== 'none' && parseFloat(cs.animationDuration) > 0.001) || (parseFloat(cs.transitionDuration) > 0.001);
      });
      return moving.length;
    });
    expect(probe).toBe(0);
    await ctx.close();
  });

  test('a pane switch crossfades: both panes are in the same grid cell and only the active one is visible', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    const panes = page.locator('section.pane');
    await expect(panes).toHaveCount(5);
    const cells = await panes.evaluateAll((els) => els.map((e) => getComputedStyle(e).gridArea));
    expect(new Set(cells).size).toBe(1);
    await expect(page.locator('section.pane[data-active=true]')).toHaveCount(1);
  });

  test('moving between the panes of a project asks the service for nothing', async ({ page }) => {
    await page.goto('/?tick=0#/projects/prj_billing');
    await expect(page.getByRole('heading', { level: 1, name: 'billing-api' })).toBeVisible();
    await page.waitForFunction(() => !document.querySelector('[aria-label^="Loading"]:not([inert] *)'));
    await page.waitForTimeout(500);
    const calls = () => page.evaluate(() => (window as unknown as { __wavesMock: { calls(): number } }).__wavesMock.calls());
    const before = await calls();
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    for (const tab of ['Requirements', 'Plan', 'Review', 'Guard', 'Overview']) {
      await nav.getByRole('link', { name: new RegExp('^' + tab) }).click();
      await expect(nav.locator('[aria-current=page]')).toHaveText(new RegExp(tab));
    }
    await page.waitForTimeout(400);
    expect(await calls()).toBe(before);
  });
});

test.describe('review fixes', () => {
  test('the decision stat card scrolls to the list and never leaves the Overview', async ({ page }) => {
    await open(page, '#/');
    await page.getByRole('button', { name: /Needs your decision/ }).first().click();
    expect(page.url()).toMatch(/#\/$/);
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  });

  test('with a broken audit chain the Approve and Reject icons are disabled', async ({ page }) => {
    await open(page, '#/', '?scenario=chain_broken');
    const row = page.getByRole('list', { name: 'Needs your decision' }).getByRole('listitem').first();
    await expect(row.getByRole('button', { name: 'Approve' })).toBeDisabled();
    await expect(row.getByRole('button', { name: 'Reject' })).toBeDisabled();
  });

  test('a phase completion started from the Overview asks for the override reason at once, together with the passphrase', async ({ page }) => {
    await open(page, '#/');
    const row = page.getByRole('list', { name: 'Needs your decision' }).getByRole('listitem').filter({ hasText: 'Content audit' });
    await row.getByRole('button', { name: 'Approve' }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByLabel(/Override reason/)).toBeVisible();
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeDisabled(); // the reason is still empty
    await dlg.getByLabel(/Override reason/).fill('Known front matter issue.');
    await expect(dlg.getByRole('button', { name: 'Approve' })).toBeEnabled();
  });

  test('a project page has no Open button: the decision text is the link', async ({ page }) => {
    await open(page, '#/projects/prj_billing');
    await expect(page.getByRole('button', { name: /^Open/ })).toHaveCount(0);
    await expect(page.getByRole('link', { name: /Decide what happens to p2-t3/ })).toHaveAttribute('href', /#\/executions\//);
  });

  test('running text is capped at 65 characters', async ({ page }) => {
    await open(page, '#/executions/exe_8f3a1c/recovery');
    const w = await page.locator('.note > div').first().evaluate((el) => el.getBoundingClientRect().width);
    const ch = await page.locator('.note > div').first().evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(w).toBeLessThanOrEqual(ch * 65);
  });

  test('the graph starts at a readable zoom and keeps its zoom bar clear of the groups', async ({ page }) => {
    await open(page, '#/graph');
    await expect(page.getByLabel('Zoom').getByText(/%$/)).toBeVisible();
    const pct = Number((await page.getByLabel('Zoom').locator('.mono').innerText()).replace('%', ''));
    expect(pct).toBeGreaterThanOrEqual(80);
  });
});

test.describe('second round of directions', () => {
  // The two Overview tables are cards since v1.2.1, so there is no status column to align.
  test('the list pages have a title and no description (Overview and Executions lead with a headline since v1.2.1); a project keeps its path and branch', async ({ page }) => {
    for (const r of ['#/projects', '#/harnesses', '#/activity', '#/settings']) {
      await open(page, r);
      await expect(page.locator('.head > div:first-child > .muted')).toHaveCount(0); // nothing under the title
    }
    await open(page, '#/projects/prj_billing');
    await expect(page.locator('.phead .meta')).toContainText('~/work/billing-api');
    await expect(page.locator('.phead .meta')).toContainText('branch main');
  });

  test('switching theme cross-fades and ends in the other palette; with reduced motion it switches at once', async ({ browser }) => {
    const ctx = await browser.newContext({ colorScheme: 'light', reducedMotion: 'reduce' });
    const page = await ctx.newPage();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
    await page.getByRole('button', { name: /^Operator/ }).click();
    await page.getByRole('menuitemradio', { name: 'Dark' }).click();
    expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(24, 22, 22)'); // no animation, no wait
    await ctx.close();
    const ctx2 = await browser.newContext({ colorScheme: 'light' });
    const p2 = await ctx2.newPage();
    await p2.goto('/');
    await p2.getByRole('button', { name: /^Operator/ }).click();
    await p2.getByRole('menuitemradio', { name: 'Dark' }).click();
    await expect.poll(() => p2.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe('rgb(24, 22, 22)');
    await ctx2.close();
  });

  test('the line to a phase that has not started is dashed, and so is the fork line; the others are solid', async ({ page }) => {
    await open(page, '#/graph');
    const dash = (sel: string) => page.locator(sel).first().evaluate((e) => getComputedStyle(e).strokeDasharray);
    await expect(page.locator('svg.edges path').first()).toBeAttached();
    expect(await dash('svg.edges path[data-tone=ahead]')).toBe('6px, 5px');
    expect(await dash('svg.edges path.fork')).toBe('6px, 5px');
    expect(await dash('svg.edges path[data-tone=ok]')).toBe('none');
    expect(await page.locator('svg.edges circle.port[data-tone=ahead]').first().evaluate((e) => getComputedStyle(e).strokeDasharray)).toBe('none');
  });

  test('on a phone the current project tab is fully in view', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await open(page, '#/projects/prj_docs/review');
    const nav = page.getByRole('navigation', { name: 'Project pages' });
    await expect(nav.locator('[aria-current=page]')).toBeVisible();
    await expect.poll(async () => { const n = (await nav.boundingBox())!, t = (await nav.locator('[aria-current=page]').boundingBox())!; return t.x >= n.x && t.x + t.width <= n.x + n.width + 1; }).toBe(true);
  });

  test('a project frame shows only its name, and the frames can be hidden', async ({ page }) => {
    await open(page, '#/graph');
    const tag = page.locator('.gtag').first();
    await expect(tag).toHaveText('billing-api');
    await expect(page.locator('.group')).toHaveCount(4);
    await page.getByLabel('Show project frames').uncheck();
    await expect(page.locator('.group')).toHaveCount(0);
    await expect(page.locator('.node').first()).toBeVisible(); // the phases stay
  });

  test('the Graph fills the panel and its tools open and close from a pane on the right', async ({ page }) => {
    await open(page, '#/graph');
    const tools = page.getByRole('complementary', { name: 'Graph tools' });
    const toggle = page.getByRole('button', { name: 'Tools', exact: true });
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(tools).toHaveCount(0);
    const wide = (await page.locator('.canvas').boundingBox())!;
    await toggle.click();
    await expect(tools).toBeVisible();
    await expect.poll(async () => (await page.locator('.canvas').boundingBox())!.width).toBeLessThan(wide.width - 200); // the pane takes its room
    await toggle.click();
    await expect(tools).toHaveCount(0);
    const vh = page.viewportSize()!.height;
    expect((await page.locator('.gfull').boundingBox())!.height).toBeGreaterThan(vh - 130); // full height, no page heading above it
  });

  test('a phase or task detail opens on the left of the tools pane, touching it', async ({ page }) => {
    await open(page, '#/graph');
    await page.getByRole('group', { name: /^P2 Persistence layer/ }).focus();
    await page.keyboard.press(' '); // Space opens the phase detail (a click in the middle of the card may hit a task row)
    const phase = page.getByRole('complementary', { name: 'Phase detail' });
    await expect(phase).toBeVisible();
    const tools = page.locator('.gside'); // the tools column, border included
    const [pb, tb] = [await phase.boundingBox(), await tools.boundingBox()];
    expect(Math.round(pb!.x + pb!.width)).toBe(Math.round(tb!.x)); // touching
    await page.getByRole('button', { name: /^p2-t3 / }).click(); // a task row
    const task = page.getByRole('complementary', { name: 'Task detail' });
    await expect(task).toBeVisible();
    await expect(task.getByText('Depends on')).toBeVisible();
    const [kb, tb2] = [await task.boundingBox(), await tools.boundingBox()];
    expect(Math.round(kb!.x + kb!.width)).toBe(Math.round(tb2!.x));
    await task.getByRole('button', { name: /^Back to/ }).click();
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toBeVisible();
  });

  test('a line can leave and enter any side of its phases, from the tools or by dragging its end', async ({ page }) => {
    await open(page, '#/graph');
    await openTools(page);
    await page.getByText(/^Line sides \(/).click(); // the list is a disclosure, closed by default
    const path = () => page.locator('svg.edges path').first().getAttribute('d');
    const before = await path();
    await page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true }).selectOption('bottom');
    await page.getByLabel('End side, line P1 to P2 in billing-api', { exact: true }).selectOption('top');
    const after = await path();
    expect(after).not.toBe(before);
    // drag the first end circle round its node to the top side
    const hit = page.locator('svg.edges circle.hit').first();
    const hb = await hit.boundingBox();
    const node = await page.getByRole('group', { name: /^P1 Schema design/ }).boundingBox();
    await page.mouse.move(hb!.x + hb!.width / 2, hb!.y + hb!.height / 2);
    await page.mouse.down();
    await page.mouse.move(node!.x + node!.width / 2, node!.y - 6, { steps: 8 });
    await page.mouse.up();
    await expect(page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true })).toHaveValue('top');
    await page.getByRole('button', { name: 'Reset lines' }).click();
    await expect(page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true })).toHaveValue('right');
  });

  test('a direct visit to the Graph opens the running phases, as a visit from another page does', async ({ page }) => {
    await page.goto('/#/graph');
    await expect(page.getByRole('group', { name: /^P2 Persistence layer/ }).getByRole('button', { name: /Collapse/ })).toBeVisible();
  });

  test('a project switched off everywhere leaves a message, not a blank canvas', async ({ page }) => {
    await open(page, '#/graph');
    for (const n of ['billing-api', 'mobile-checkout', 'docs-site', 'billing-api-refund']) await page.getByRole('button', { name: n, exact: true }).click();
    await expect(page.getByText(/No project is drawn/)).toBeVisible();
  });

  test('selecting a phase keeps it in view although the detail pane takes room', async ({ page }) => {
    await open(page, '#/graph');
    const node = page.getByRole('group', { name: /^P4 Hardening/ });
    await node.focus();
    await page.keyboard.press(' ');
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toBeVisible();
    await page.waitForTimeout(500);
    const nb = (await node.boundingBox())!;
    const pane = (await page.getByRole('complementary', { name: 'Phase detail' }).boundingBox())!;
    expect(nb.x + nb.width).toBeLessThanOrEqual(pane.x + 1); // not hidden under the pane
  });

  // The strip beside the closed tools is gone since v1.2.1: the toggle sits in the capsule under the canvas.
  test('a line that leaves the bottom of a phase starts at its real bottom edge, and the choice survives leaving the page', async ({ page }) => {
    await open(page, '#/graph');
    await openTools(page);
    await page.getByText(/^Line sides \(/).click();
    await page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true }).selectOption('bottom');
    const node = page.getByRole('group', { name: /^P1 Schema design/ });
    const nb = (await node.boundingBox())!;
    const start = await page.locator('svg.edges circle.port').first().boundingBox();
    expect(Math.abs(start!.y + start!.height / 2 - (nb.y + nb.height))).toBeLessThan(3); // on the card's bottom border, whatever its height
    await page.waitForTimeout(500);
    await page.evaluate(() => (location.hash = '/projects'));
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await page.evaluate(() => (location.hash = '/graph'));
    await openTools(page);
    await page.getByText(/^Line sides \(/).click();
    await expect(page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true })).toHaveValue('bottom');
  });

  test('the Graph draws without a script or SVG error, on a direct visit and after a change of view', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push('pageerror ' + e.message));
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
    await page.goto('/#/graph');
    await expect(page.locator('.node').first()).toBeVisible();
    await page.getByRole('button', { name: 'Table', exact: true }).click();
    await page.getByRole('button', { name: 'Graph', exact: true }).click();
    await expect(page.locator('.node').first()).toBeVisible();
    expect(errors).toEqual([]);
  });

  test('a line end slides along the edge of its phase instead of snapping, and keeps its place after a reload of the layout', async ({ page }) => {
    await open(page, '#/graph');
    const hit = page.locator('svg.edges circle.hit').first(); // start of P1 to P2, on the right side of P1
    const node = (await page.getByRole('group', { name: /^P1 Schema design/ }).boundingBox())!;
    const hb = (await hit.boundingBox())!;
    const cx = hb.x + hb.width / 2, cy = hb.y + hb.height / 2;
    const ys: number[] = [];
    await page.mouse.move(cx, cy);
    await page.mouse.down();
    for (const f of [0.25, 0.4, 0.7]) {
      await page.mouse.move(node.x + node.width + 2, node.y + node.height * f, { steps: 4 });
      ys.push(Math.round(((await page.locator('svg.edges circle.port').first().boundingBox())!.y)));
    }
    await page.mouse.up();
    expect(new Set(ys).size).toBe(3); // a different place for each position, not one of four snap points
    await openTools(page);
    await page.getByText(/^Line sides \(/).click();
    await expect(page.getByLabel('Start side, line P1 to P2 in billing-api', { exact: true })).toHaveValue('right');
  });

  test('the tools slide: halfway through, the column is between its open and closed widths', async ({ page }) => {
    await open(page, '#/graph');
    const side = page.locator('.gside');
    expect((await side.boundingBox())!.width).toBe(0);
    await openTools(page);
    await page.waitForTimeout(60);
    const mid = (await side.boundingBox())!.width;
    expect(mid).toBeGreaterThan(0);
    expect(mid).toBeLessThan(288);
    await page.waitForTimeout(500);
    expect((await side.boundingBox())!.width).toBe(288);
    await expect(page.getByRole('button', { name: 'Tools', exact: true })).toHaveAttribute('aria-expanded', 'true');
  });

  test('the detail pane slides in and out, and keeps its content while it leaves', async ({ page }) => {
    await open(page, '#/graph');
    const col = page.locator('.gdetail');
    expect((await col.boundingBox())!.width).toBe(0);
    await page.getByRole('group', { name: /^P2 Persistence layer/ }).focus();
    await page.keyboard.press(' ');
    await page.waitForTimeout(60);
    const mid = (await col.boundingBox())!.width;
    expect(mid).toBeGreaterThan(0);
    expect(mid).toBeLessThan(341);
    await page.waitForTimeout(400);
    expect((await col.boundingBox())!.width).toBe(341);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(60);
    await expect(page.locator('.gpane h2').first()).toContainText('Persistence layer'); // still showing it while it slides out
    await page.waitForTimeout(400);
    expect((await col.boundingBox())!.width).toBe(0);
    await expect(page.getByRole('complementary', { name: 'Phase detail' })).toBeHidden();
  });

  test('project overview: the journey leads, the tasks sit left of the decisions, all starting in the first screen', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await open(page, '#/projects/prj_mobile');
    const journey = (await page.getByRole('region', { name: 'Phases' }).boundingBox())!;
    const tasks = (await page.getByRole('region', { name: /^Tasks in/ }).boundingBox())!;
    const waiting = (await page.getByRole('region', { name: 'Pending approvals' }).boundingBox())!;
    expect(tasks.y).toBeGreaterThanOrEqual(journey.y + journey.height);
    expect(tasks.x + tasks.width).toBeLessThanOrEqual(waiting.x);
    expect(waiting.y).toBeLessThan(900);
    await expect(page.getByRole('region', { name: 'Versions' })).toBeVisible();
  });

  test('Add requirement and Request planning sit at the right end of their table header', async ({ page }) => {
    await open(page, '#/projects/prj_mobile/requirements');
    const head = (await page.getByRole('region', { name: 'Sources' }).locator('.ph').boundingBox())!;
    const add = (await page.getByRole('button', { name: 'Add requirement' }).boundingBox())!;
    expect(add.y).toBeGreaterThanOrEqual(head.y);
    expect(add.y + add.height).toBeLessThanOrEqual(head.y + head.height);
    expect(Math.round(head.x + head.width - (add.x + add.width))).toBeLessThanOrEqual(24);
    await open(page, '#/projects/prj_mobile/plan');
    // A waiting version loads above this panel (since 2026-11-27): measure after it is there.
    await expect(page.getByText('Approve workflow version 2')).toBeVisible();
    const ph = (await page.getByRole('region', { name: 'Planning request' }).locator('.ph').boundingBox())!;
    const rq = (await page.getByRole('button', { name: 'Request planning' }).boundingBox())!;
    expect(rq.y + rq.height).toBeLessThanOrEqual(ph.y + ph.height);
    expect(Math.round(ph.x + ph.width - (rq.x + rq.width))).toBeLessThanOrEqual(24);
  });

  // The side column of the old Overview is gone since v1.2.1.
  test('the fork label: shown as a badge, changed in a dialog that offers no loop and no no-op save', async ({ page }) => {
    await open(page, '#/projects/prj_refund');
    await expect(page.getByText('Fork of billing-api').first()).toBeVisible();
    await page.getByRole('button', { name: 'Fork', exact: true }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByRole('heading', { name: 'Change the original of billing-api-refund' })).toBeVisible();
    await expect(dlg.getByRole('button', { name: 'Save' })).toBeDisabled(); // nothing changed
    await dlg.getByLabel('Original project').selectOption('');
    await dlg.getByRole('button', { name: 'Save' }).click();
    await expect(dlg).toBeHidden();
    await expect(page.getByText('Fork of billing-api')).toHaveCount(0);
    // billing-api is the original of nothing now, and billing-api-refund is not offered as the original of billing-api once it is a fork of it
    await page.getByRole('button', { name: 'Fork', exact: true }).click();
    await page.getByLabel('Original project').selectOption({ label: 'billing-api' });
    await page.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText('Fork of billing-api').first()).toBeVisible();
    await open(page, '#/projects/prj_billing');
    await page.getByRole('button', { name: 'Fork', exact: true }).click();
    const options = await page.getByLabel('Original project').locator('option').allTextContents();
    expect(options).not.toContain('billing-api-refund');
    expect(options).toContain('mobile-checkout');
  });

  test('Executions says one thing about a waiting harness: the timeline, the filter and the card agree', async ({ page }) => {
    await open(page, '#/executions');
    const row = page.getByRole('list', { name: 'Timeline' }).getByRole('listitem').filter({ hasText: 'Refund validation rules' });
    await expect(row.getByText('Waiting for instruction')).toBeVisible();
    await expect(row.getByText('Running')).toHaveCount(0);
    await expect(page.getByRole('list', { name: 'Needs attention' }).getByRole('listitem').filter({ hasText: 'Refund validation rules' })).toContainText('Waiting for instruction');
    await expect(page.getByRole('button', { name: /^Waiting 1/ })).toBeVisible();
    await page.getByRole('button', { name: /^Waiting 1/ }).click();
    await expect(row).toBeVisible();
    await expect(page.getByRole('list', { name: 'Timeline' }).getByRole('listitem').filter({ hasText: 'Implement refund endpoint' })).toHaveCount(0);
  });

  test('a project connected before the waiting hooks existed says so, and a fresh connection does not', async ({ page }) => {
    await open(page, '#/harnesses');
    // billing-api is the seed's old connection (guard hook, no waiting hooks)
    await expect(page.getByText('Waiting for instruction is not active here.')).toBeVisible();
    await expect(page.getByText('Waiting for instruction is not active here.')).toHaveCount(1);
  });

  test('Settings has a place for the longest wait, behind the passphrase', async ({ page }) => {
    await open(page, '#/settings');
    await expect(page.getByText('Longest wait for instruction')).toBeVisible();
    await expect(page.getByText(/may wait up to 8 h/)).toBeVisible();
  });

  test('the Harnesses notice offers Connect again, which opens the plan', async ({ page }) => {
    await open(page, '#/harnesses');
    await page.getByRole('link', { name: 'Connect again' }).click();
    await expect(page).toHaveURL(/#\/harnesses\/connect\/prj_billing/);
  });
});


test.describe('stage 6: pages merged into the project', () => {
  test('the Plan pane holds the workflow: submit a draft, pick another version, decide it where it waits', async ({ page }) => {
    await open(page, '#/projects/prj_billing/plan');
    await expect(page.getByRole('heading', { level: 2, name: /Workflow · version 3/ })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Submit' })).toBeVisible(); // version 3 is a draft
    await page.getByRole('button', { name: 'Version 2' }).click();
    await expect(page.getByRole('heading', { level: 2, name: /Workflow · version 2/ })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Revise' })).toBeVisible(); // version 2 is approved
    await open(page, '#/projects/prj_mobile/plan');
    await expect(page.getByRole('region', { name: /Workflow · mobile-checkout/ })).toBeVisible(); // the waiting version carries its own decision
    await expect(page.getByRole('button', { name: 'Approve' })).toBeEnabled();
  });

  test('the Guard pane tells the state of the guard and what it does not defend', async ({ page }) => {
    await open(page, '#/projects/prj_billing/guard');
    await expect(page.getByRole('region', { name: 'Guard status' })).toContainText('What this does not defend');
    await expect(page.getByRole('region', { name: 'Guard status' })).toContainText(/Policy: (audit|enforce|off)/);
  });

  test('the Graph names a project whose proposal waits, and the link goes to its Plan pane', async ({ page }) => {
    await open(page, '#/graph');
    const link = page.getByRole('link', { name: /Proposal ready/ });
    await expect(link).toHaveAttribute('href', /#\/projects\/prj_mobile\/plan$/);
  });

  test('a tab panel that takes keyboard focus shows a focus ring, and the page asks for the full screen on a phone', async ({ page }) => {
    await open(page, '#/harnesses');
    const panel = page.getByRole('tabpanel').first();
    await panel.focus();
    expect(await panel.evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe('none');
    expect(await page.locator('meta[name=viewport]').getAttribute('content')).toContain('viewport-fit=cover');
  });

  test('a hidden pane gives back its height: the Overview is not as tall as the Plan', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await open(page, '#/projects/prj_billing/plan');
    await expect(page.getByRole('heading', { level: 2, name: /Workflow · version/ })).toBeVisible(); // the tall pane is loaded
    const plan = (await page.locator('.panes').boundingBox())!.height;
    await page.getByRole('navigation', { name: 'Project pages' }).getByRole('link', { name: /^Overview/ }).click();
    await expect.poll(async () => (await page.locator('.panes').boundingBox())!.height).toBeLessThan(plan - 200);
  });

  test('inside a project the Guard pane has its own head, no way back to Harnesses, and keeps the policy note honest', async ({ page }) => {
    await open(page, '#/projects/prj_billing/guard');
    await expect(page.getByRole('heading', { level: 2, name: 'Guard', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('link', { name: 'Connect' })).toHaveCount(0); // no second breadcrumb
    await expect(page.getByRole('button', { name: 'Cancel' })).toHaveCount(0);
  });

  test('the confirmation of an icon decision shows everything the approval carries, not only its phase', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await expect(dlg.getByText('Execution', { exact: true })).toBeVisible(); // a fact the agent's result carries
    await expect(dlg.getByText('exe_b220f1')).toBeVisible();
    await expect(dlg.getByText('Task', { exact: true })).toBeVisible();
  });
});

// v1.2.1: one 72 px rail replaces the 232 px sidebar and its thin mode on the Graph, so those four tests became these.
test.describe('rail', () => {
  const PAGES = ['Overview', 'Graph', 'Projects', 'Executions', 'Harnesses', 'Activity', 'Settings'];

  test('the rail is 72 px on every page, each icon is a named 44 px target, and the Graph has no title text in its corner', async ({ page }) => {
    for (const hash of ['#/', '#/graph', '#/projects']) {
      await open(page, hash);
      const side = page.locator('aside.side');
      expect((await side.boundingBox())!.width).toBe(72);
      for (const n of PAGES) {
        const link = side.getByRole('link', { name: new RegExp('^' + n) });
        await expect(link.locator('svg')).toBeVisible();
        const box = (await link.boundingBox())!;
        expect([box.width, box.height]).toEqual([44, 44]);
        expect((await link.locator('.lbl').boundingBox())!.width).toBeLessThanOrEqual(1); // named for a screen reader, not drawn
      }
    }
    await open(page, '#/graph');
    await expect(page.locator('.gtitle')).toHaveCount(0);
  });

  test('the Overview icon carries the signal dot while an approval waits, and not when nothing does', async ({ page }) => {
    await open(page, '#/');
    await expect(page.locator('aside.side').getByRole('link', { name: /^Overview.*decisions waiting/ }).locator('.sig')).toBeVisible();
    await open(page, '#/', '?scenario=empty');
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
    await expect(page.locator('.sig')).toHaveCount(0);
  });

  test('the account menu opens from the keyboard beside the rail', async ({ page }) => {
    await open(page, '#/');
    await page.getByRole('button', { name: /^Operator/ }).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('menu')).toBeVisible();
    expect((await page.getByRole('menu').boundingBox())!.x).toBeGreaterThanOrEqual(44);
    await expect(page.getByRole('menuitem', { name: 'Sign out' })).toBeVisible();
  });

  test('on a phone the Graph controls sit above its table, not over it', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await open(page, '#/graph');
    await expect(page.getByRole('list', { name: 'Phases' }).getByRole('listitem').first()).toBeVisible();
    const bar = (await page.locator('.gbar').boundingBox())!;
    const table = (await page.locator('.gtable .panel').boundingBox())!;
    expect(table.y).toBeGreaterThanOrEqual(bar.y + bar.height - 1);
  });

  test('on a phone the rail is a drawer that shows the page names', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await open(page, '#/');
    await page.getByRole('button', { name: 'Menu' }).click();
    const label = page.locator('aside.side').getByText('Executions', { exact: true });
    await expect(label).toBeVisible();
    expect((await label.boundingBox())!.width).toBeGreaterThan(40);
  });

  // Changed 2026-11-27 (human): the rail opens and shows the names inside itself, instead of a label floating beside it.
  test('a rail icon shows its name when the keyboard reaches it', async ({ page }) => {
    await open(page, '#/projects');
    await page.getByRole('link', { name: /Waves, overview/ }).focus();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Tab'); // Overview, then Graph
    const link = page.locator('aside.side').getByRole('link', { name: 'Graph', exact: true });
    await expect(link).toBeFocused();
    const label = (await link.locator('.lbl').boundingBox())!;
    const at = (await link.boundingBox())!;
    await expect.poll(async () => (await link.locator('.lbl').boundingBox())!.width).toBeGreaterThan(30);
    await expect.poll(async () => (await page.locator('aside.side').boundingBox())!.width).toBeGreaterThan(180);
    expect(Math.abs(label.y + label.height / 2 - (at.y + at.height / 2))).toBeLessThan(4);
  });

  // Changed 2026-11-27 (human): secondary buttons are solid instead of outlined.
  test('a secondary button is solid, not an outline', async ({ page }) => {
    await open(page, '#/');
    const [bg, border] = await page.getByRole('button', { name: 'Elevate' }).evaluate((e) => [getComputedStyle(e).backgroundColor, getComputedStyle(e).borderTopColor]);
    expect(bg).toBe('rgb(218, 227, 223)');
    expect(border).toBe('rgba(0, 0, 0, 0)');
  });

  test('the fonts come from Waves itself', async ({ page }) => {
    const fonts: string[] = [];
    page.on('request', (r) => { if (r.url().includes('.woff2')) fonts.push(new URL(r.url()).host); });
    await open(page, '#/');
    await expect.poll(() => page.evaluate(() => document.fonts.ready.then(() => document.fonts.check('800 20px "M PLUS Rounded 1c"')))).toBe(true);
    expect(fonts.length).toBeGreaterThan(0);
    for (const h of fonts) expect(h).toBe(new URL(page.url()).host);
  });

  // Changed 2026-11-27 (human): the running part is a wave whose length swells on the way; its shape is tested in v121.spec.
  test('a running bar moves left and right, and stands still and full with reduced motion', async ({ page }) => {
    await open(page, '#/');
    const bar = page.locator('.bar.run').first();
    await expect(bar).toBeVisible();
    const style = () => bar.locator('i').evaluate((e) => { const c = getComputedStyle(e); return [c.animationName.split(', ')[0], c.animationDirection.split(', ')[0]]; });
    expect(await style()).toEqual(['w-swell', 'alternate']);
    const windows = new Set<string>();
    for (let n = 0; n < 4; n++) { windows.add(await bar.locator('i').evaluate((e) => getComputedStyle(e).clipPath)); await page.waitForTimeout(250); }
    expect(windows.size).toBeGreaterThan(2);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    expect((await style())[0]).toBe('none');
    expect((await bar.locator('i').boundingBox())!.width).toBeCloseTo((await bar.boundingBox())!.width, 0);
  });
});
