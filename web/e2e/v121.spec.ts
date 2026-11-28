import { test, expect, type Locator, type Page } from '@playwright/test';

const PASS = 'mock-passphrase-ui-review'; // guard:allow fake fixture of the mock adapter
const open = async (page: Page, hash: string, query = '') => {
  await page.goto('/' + query + hash);
  await expect(page.locator('main')).toBeVisible();
};
const box = async (l: Locator) => (await l.boundingBox())!;
const css = (l: Locator, p: string, pseudo?: string) => l.evaluate((e, [prop, ps]) => getComputedStyle(e, ps || null).getPropertyValue(prop!), [p, pseudo ?? '']);
const middle = async (l: Locator) => { const b = await box(l); return b.y + b.height / 2; };

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

test.describe('the first line of a page', () => {
  test('on a wide screen the crumb, the tools and the page action share one line; on a phone the page keeps them', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    const header = page.locator('header.top');
    const register = page.getByRole('link', { name: 'Register project' });
    await expect(header.getByText('Overview', { exact: true })).toBeVisible();
    await expect(header.getByRole('link', { name: 'Register project' })).toBeVisible();
    expect(Math.abs((await middle(register)) - (await middle(page.getByRole('button', { name: 'Search or jump to' }))))).toBeLessThan(2);
    // Resizing across the breakpoint moves the line into the page and back, and never doubles it.
    await page.setViewportSize({ width: 390, height: 800 });
    await expect(page.locator('main').getByRole('link', { name: 'Register project' })).toBeVisible();
    await expect(register).toHaveCount(1);
    await expect(page.getByRole('button', { name: 'Elevate' })).toBeHidden(); // it is in the account menu of the drawer
    await page.setViewportSize({ width: 1360, height: 900 });
    await expect(header.getByRole('link', { name: 'Register project' })).toBeVisible();
    await expect(register).toHaveCount(1);
  });

  test('moving between pages leaves no first line behind', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    const rows = page.locator('.tcrumb');
    await expect(rows).toHaveCount(1);
    for (const [hash, says] of [['#/executions', 'Executions'], ['#/projects/prj_billing', '← Projects'], ['#/projects/prj_docs/review', '← docs-site'], ['#/', 'Overview']]) {
      await page.evaluate((h) => { location.hash = h; }, hash!);
      await expect(rows).toHaveText(says!); // one line, and it is the new page's
    }
    await page.evaluate(() => { location.hash = '#/settings'; });
    await expect(rows).toHaveCount(0);
  });

  test('the keyboard meets the line in the order it is drawn, and wide tabs wait for room', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    await page.getByRole('button', { name: 'Elevate' }).focus();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('link', { name: 'Register project' })).toBeFocused();
    await open(page, '#/projects/prj_docs/review');
    const nav = page.locator('header.top').getByRole('navigation', { name: 'Project pages' });
    await expect(nav).toBeVisible();
    await page.setViewportSize({ width: 1100, height: 900 });
    await expect(nav).toHaveCount(0);
    await expect(page.locator('main').getByRole('navigation', { name: 'Project pages' })).toBeVisible();
  });

  test('a phase under review leads the page: the crumb names the project and the tabs stay reachable', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    await expect(page.getByRole('link', { name: '← docs-site' })).toHaveAttribute('href', /#\/projects\/prj_docs$/);
    await expect(page.getByRole('navigation', { name: 'Project pages' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Fork', exact: true })).toHaveCount(0);
  });
});

test.describe('the Graph', () => {
  test.beforeEach(async ({ page }) => page.setViewportSize({ width: 1360, height: 900 }));

  test('has one bar of its own, and the top row returns while the session is unlocked', async ({ page }) => {
    await open(page, '#/graph');
    await expect(page.locator('.node').first()).toBeVisible();
    await expect(page.locator('header.top')).toBeHidden();
    await expect(page.locator('.gbar').getByRole('status')).toHaveText('Live');
    await page.getByRole('button', { name: /^Operator/ }).click();
    await page.getByRole('menuitem', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill('mock-passphrase-ui-review'); // guard:allow fake fixture of the mock adapter
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
    await expect(page.getByRole('button', { name: /^Unlocked until/ })).toBeVisible();
    await expect(page.locator('.gbar').getByRole('status')).toBeHidden(); // one connection state, not two
  });

  test('keeps the top row when it has something to say', async ({ page }) => {
    await page.goto('/?scenario=chain_broken#/graph');
    await expect(page.locator('header.top')).toBeVisible();
  });

  test('a task list stays inside its node, and a focused frame chip is brought into view', async ({ page }) => {
    await open(page, '#/graph');
    const node = page.locator('.node').filter({ has: page.locator('.trow') }).first();
    await expect(node.locator('.trow').first()).toBeVisible();
    const n = await box(node);
    for (const row of await node.locator('.trow').all()) {
      const r = await box(row);
      expect(r.x + r.width).toBeLessThanOrEqual(n.x + n.width);
    }
    const chip = page.getByRole('link', { name: /open the Review of docs-site$/ });
    await chip.focus();
    await expect(chip).toBeInViewport({ ratio: 1 });
  });
});

test('changes outside the plan show wherever a phase can be approved', async ({ page }) => {
  await open(page, '#/');
  const card = page.getByRole('article').filter({ hasText: 'P1 Content audit' });
  await expect(card.getByText('2 changes outside the plan')).toBeVisible();
  await open(page, '#/projects/prj_docs/review');
  await expect(page.getByText('2 changes outside the plan. Read the list')).toBeVisible();
  await open(page, '#/graph');
  await page.getByRole('group', { name: /^P1 Content audit/ }).focus();
  await page.keyboard.press('Space');
  await expect(page.getByText('Outside the plan', { exact: true })).toBeVisible();
});

test.describe('polish', () => {
  test('the Plan pane puts a waiting decision first and pairs a version with its own approval', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/projects/prj_mobile/plan');
    const plan = page.getByRole('region', { name: 'Plan', exact: true });
    const heading = plan.getByRole('heading', { name: /Workflow · version 2/ });
    await expect(heading).toBeVisible();
    expect((await box(heading)).y).toBeLessThan((await box(plan.getByRole('heading', { name: 'Planning request' }))).y);
    await expect(plan.getByRole('heading', { name: 'Latest proposal' })).toHaveCount(0); // there is none
    await plan.getByRole('button', { name: 'Version 1' }).click();
    await plan.getByRole('button', { name: 'Revise' }).click();
    await plan.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(plan.getByRole('heading', { name: /Workflow · version 3/ })).toBeVisible();
    await expect(plan.getByText('Approve workflow version 3')).toBeVisible(); // two versions wait: the card is the shown one's
    await plan.getByRole('button', { name: 'Version 2' }).click();
    await expect(plan.getByText('Approve workflow version 2')).toBeVisible();
    await expect(plan.getByText('Approve workflow version 3')).toHaveCount(0);
  });

  test('a lost connection is said in words across the page, and goes away when it returns', async ({ page }) => {
    await page.clock.install();
    await open(page, '#/');
    await expect(page.getByRole('status').filter({ hasText: 'Live' })).toBeVisible();
    await page.evaluate(() => (window as any).__wavesMock.refuse(true));
    const banner = page.getByRole('alert').filter({ hasText: 'The connection to Waves has been lost' });
    await expect(banner).toHaveCount(0); // a short drop is not announced across the page
    await page.clock.fastForward(31_000);
    await expect(banner).toBeVisible();
    await page.evaluate(() => (window as any).__wavesMock.refuse(false));
    await page.clock.fastForward(60_000);
    await expect(banner).toHaveCount(0);
  });

  test('the older executions say not started, and no note repeats the queued one', async ({ page }) => {
    await open(page, '#/executions');
    const row = page.getByRole('row').filter({ hasText: 'exe_d5e8a0' });
    await expect(row).toContainText('not started');
    await expect(page.getByText(/queued\. Open/)).toHaveCount(0);
  });

  test('the sample log of a connected execution has no heartbeat', async ({ page }) => {
    await page.goto('/?tick=300#/executions/exe_a07e52');
    const log = page.getByRole('log', { name: 'Execution log' });
    await expect(log).toContainText('Persistence covers the cold start path', { timeout: 6000 });
    await expect(log).not.toContainText(/heartbeat|ttl \d+/);
  });

  test('crossing the narrow breakpoint keeps the keyboard focus on a page action', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    const register = page.getByRole('link', { name: 'Register project' });
    await register.focus();
    await page.setViewportSize({ width: 700, height: 900 });
    await expect(page.locator('main').getByRole('link', { name: 'Register project' })).toBeFocused();
  });
});

test.describe('a version and its approval', () => {
  const plan = (page: Page) => page.getByRole('region', { name: 'Plan', exact: true });
  // A second version of the same content waits: copy version 2 and submit the copy.
  async function twoWaiting(page: Page) {
    await plan(page).getByRole('button', { name: 'Version 1' }).click();
    await plan(page).getByRole('button', { name: 'Revise' }).click();
    await plan(page).getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(plan(page).getByText('Approve workflow version 3')).toBeVisible();
  }

  test('never shows the other version\'s card while the new one loads', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await page.goto('/?latency=700#/projects/prj_mobile/plan');
    await twoWaiting(page);
    await plan(page).getByRole('button', { name: 'Version 2' }).click();
    await expect(plan(page).getByRole('heading', { name: /Workflow · version 2/ })).toBeVisible();
    expect(await plan(page).getByText('Approve workflow version 3').count()).toBe(0); // checked at once, no waiting
  });

  test('two waiting versions with the same content keep their own approvals', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/projects/prj_mobile/plan');
    await twoWaiting(page); // versions 3 and then 4 are both copies of version 1
    await twoWaiting2(page);
    await plan(page).getByRole('button', { name: 'Version 3' }).click();
    await expect(plan(page).getByText('Approve workflow version 3')).toBeVisible();
    await expect(plan(page).getByText('Approve workflow version 4')).toHaveCount(0);
  });

  async function twoWaiting2(page: Page) {
    await plan(page).getByRole('button', { name: 'Version 1' }).click();
    await plan(page).getByRole('button', { name: 'Revise' }).click();
    await plan(page).getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(plan(page).getByText('Approve workflow version 4')).toBeVisible();
  }
});

// Human's five notes of 2026-11-27: a rail that opens with names, waves on running bars, solid coloured buttons that change
// colour on hover, and a highlight that follows the pointer across the four numbers.
test.describe('hover and motion', () => {
  test.beforeEach(async ({ page }) => page.setViewportSize({ width: 1360, height: 900 }));

  test('the rail opens over the page on hover and shows every name, and closes again', async ({ page }) => {
    await open(page, '#/executions');
    const side = page.locator('aside.side');
    const heading = page.getByRole('heading', { level: 1 });
    const at = (await box(heading)).x;
    await side.hover({ position: { x: 30, y: 300 } });
    await expect.poll(async () => (await box(side)).width).toBeGreaterThanOrEqual(200);
    for (const name of ['Overview', 'Graph', 'Projects', 'Executions', 'Harnesses', 'Activity', 'Settings']) {
      const label = side.getByRole('link', { name: new RegExp('^' + name) }).locator('.lbl').first();
      await expect.poll(async () => (await box(label)).width).toBeGreaterThan(40);
      await expect.poll(() => css(label, 'opacity')).toBe('1');
    }
    expect((await box(heading)).x).toBe(at); // it lies over the page, nothing moves under the reader
    await page.mouse.move(700, 500);
    await expect.poll(async () => (await box(side)).width).toBe(72);
  });

  // Human, 2026-11-28: smoother, opening and closing.
  test('the rail glides open and closed, waits a moment before closing, and the names are never squeezed', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    const side = page.locator('aside.side');
    const label = side.getByRole('link', { name: /^Executions/ }).locator('.lbl');
    const timing = async () => [await css(side, 'transition-duration'), await css(side, 'transition-delay')];
    expect(await timing()).toEqual(['0.34s', '0.12s']); // closing: a short wait, so a slip of the pointer does not snap it shut
    await side.hover({ position: { x: 30, y: 300 } });
    expect(await timing()).toEqual(['0.42s', '0.06s']);
    // Part of the way: the name already has its whole width (the rail uncovers it) and is fading in.
    await page.waitForTimeout(150);
    const midway = { rail: (await box(side)).width, name: (await box(label)).width, shown: Number(await css(label, 'opacity')) };
    await expect.poll(async () => (await box(side)).width).toBe(208);
    await expect.poll(() => css(label, 'opacity')).toBe('1');
    expect(midway.rail).toBeLessThan(208);
    expect(midway.name).toBeCloseTo((await box(label)).width, 0);
    expect(midway.shown).toBeLessThan(1);
    await page.mouse.move(700, 500);
    await page.waitForTimeout(60);
    expect((await box(side)).width).toBe(208); // still open during the wait
    await page.waitForTimeout(190);
    const leaving = { rail: (await box(side)).width, name: (await box(label)).width };
    expect(leaving.rail).toBeGreaterThan(72);
    expect(leaving.rail).toBeLessThan(208);
    expect(leaving.name).toBeGreaterThan(40); // it fades out whole
    await expect.poll(async () => (await box(side)).width).toBe(72);
    await expect.poll(async () => (await box(label)).width).toBeLessThanOrEqual(1);
  });

  test('an open account menu keeps the rail open', async ({ page }) => {
    await open(page, '#/');
    const side = page.locator('aside.side');
    await page.getByRole('button', { name: /^Operator/ }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).hover();
    await expect.poll(async () => (await box(side)).width).toBeGreaterThanOrEqual(200);
  });

  test('buttons are solid, each action in its own colour, and change colour under the pointer with readable text', async ({ page }) => {
    for (const scheme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await open(page, '#/executions');
      const cards = page.getByRole('list', { name: 'Needs attention' });
      const seen = new Map<string, string>();
      const paint = (l: Locator) => l.evaluate((e) => {
        const cv = document.createElement('canvas'); cv.width = cv.height = 1;
        const cx = cv.getContext('2d')!;
        const rgb = (c: string) => { cx.clearRect(0, 0, 1, 1); cx.fillStyle = c; cx.fillRect(0, 0, 1, 1); return [...cx.getImageData(0, 0, 1, 1).data]; };
        const s = getComputedStyle(e);
        const lum = (c: number[]) => { const f = (v: number) => (v / 255 <= 0.03928 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4); return 0.2126 * f(c[0]!) + 0.7152 * f(c[1]!) + 0.0722 * f(c[2]!); };
        const bg = rgb(s.backgroundColor), fg = rgb(s.color);
        const [a, b] = [lum(bg), lum(fg)].sort((x, y) => y - x);
        return { bg: s.backgroundColor, alpha: bg[3]!, ratio: (a! + 0.05) / (b! + 0.05) };
      });
      const check = async (name: string, l: Locator) => {
        await page.mouse.move(700, 890);
        await page.waitForTimeout(300);
        const rest = await paint(l);
        await l.hover();
        await page.waitForTimeout(300);
        const over = await paint(l);
        expect(rest.alpha, `${scheme} ${name} is solid`).toBe(255);
        expect(over.bg, `${scheme} ${name} changes colour on hover`).not.toBe(rest.bg);
        for (const x of [rest, over]) expect(x.ratio, `${scheme} ${name} text contrast`).toBeGreaterThanOrEqual(4.5);
        seen.set(name, rest.bg);
      };
      await check('Release', cards.getByRole('button', { name: /^Release/ }));
      await check('Retry', cards.getByRole('button', { name: /^Retry/ }));
      await check('Resolve', cards.getByRole('link', { name: /^Resolve/ }));
      await check('Open execution', cards.getByRole('link', { name: /^Open execution/ }));
      await open(page, '#/');
      const card = page.getByRole('article').filter({ hasText: 'P1 Content audit' });
      await check('Approve', card.getByRole('button', { name: /^Approve/ }));
      await check('Reject', card.getByRole('button', { name: /^Reject/ }));
      expect(new Set(seen.values()).size, `${scheme}: six actions, six colours`).toBe(6);
    }
  });

  test('a running bar on the Overview is a wave that is short at the ends and long in the middle', async ({ page }) => {
    await open(page, '#/');
    const fill = page.locator('.bar.run > i').first();
    await expect(fill).toBeVisible();
    expect(await css(fill, 'mask-image', '::before')).toContain('data:image/svg+xml');
    expect(await css(fill, 'animation-name', '::before')).toBe('w-flow');
    // Slow (human, 2026-11-28), and the water moves by transform so it stays smooth between pixels.
    expect(parseFloat(await css(fill, 'animation-duration'))).toBeGreaterThanOrEqual(2.5);
    expect(parseFloat(await css(fill, 'animation-duration', '::before'))).toBeGreaterThanOrEqual(2);
    // The visible window of the wave, as a share of the bar, at the start, the middle and the end of one pass.
    const shown = await fill.evaluate((e) => {
      const a = e.getAnimations().find((x) => (x as CSSAnimation).animationName === 'w-swell')!;
      a.pause();
      const d = a.effect!.getTiming().duration as number;
      return [0, d / 2, d - 1].map((t) => {
        a.currentTime = t;
        const [, right, , left] = getComputedStyle(e).clipPath.slice('inset('.length, -1).split(' ').map(parseFloat);
        return { left: left!, width: 100 - right! - left! };
      });
    });
    const [start, mid, end] = shown as [{ left: number; width: number }, { left: number; width: number }, { left: number; width: number }];
    expect(start.left).toBeLessThan(mid.left);
    expect(mid.left).toBeLessThan(end.left);
    expect(mid.width).toBeGreaterThan(start.width * 2);
    expect(mid.width).toBeGreaterThan(end.width * 2);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    expect(await css(fill, 'animation-name')).toBe('none');
    expect(await css(fill, 'animation-name', '::before')).toBe('none');
    expect(await css(fill, 'clip-path')).toBe('none'); // still and whole
  });

  // Changed 2026-11-28 (human): one slow, smooth wave. It is a layer of its own that moves by transform, and one period of it
  // is the height of the bar.
  test('a running execution on the timeline ends in one slow wave; finished ones do not', async ({ page }) => {
    await open(page, '#/executions');
    const timeline = page.getByRole('list', { name: 'Timeline' });
    const run = timeline.locator('.tl-bar[data-g="running"]').first();
    await expect(run).toBeVisible();
    expect(await css(run, 'mask-image', '::after')).toContain('data:image/svg+xml');
    expect(await css(run, 'animation-name', '::after')).toBe('w-tide');
    expect(parseFloat(await css(run, 'animation-duration', '::after'))).toBeGreaterThanOrEqual(1.5);
    expect(await css(run, 'mask-size', '::after')).toBe(`12px ${(await box(run)).height}px`); // one wave over the height
    expect(await css(timeline.locator('.tl-bar[data-g="completed"]').first(), 'content', '::after')).toBe('none');
    await page.emulateMedia({ reducedMotion: 'reduce' });
    expect(await css(run, 'animation-name', '::after')).toBe('none');
  });

  // Human, 2026-11-28: no lines above the numbers.
  test('the four numbers have no line above them', async ({ page }) => {
    await open(page, '#/');
    const tiles = page.getByRole('group', { name: 'Status now' }).locator('[data-tone]');
    await expect(tiles).toHaveCount(4);
    for (const tile of await tiles.all()) expect(await css(tile, 'border-top-width')).toBe('0px');
  });

  test('a highlight follows the pointer across the four numbers, takes each one\'s colour and can be turned back midway', async ({ page }) => {
    await open(page, '#/');
    const strip = page.getByRole('group', { name: 'Status now' });
    const bubble = page.locator('.hoverbubble');
    await expect(bubble).toHaveCSS('opacity', '0');
    const tiles = strip.locator('> *');
    await tiles.nth(0).hover();
    await expect(bubble).toHaveCSS('opacity', '1');
    const colour = () => css(bubble, 'background-color');
    const near = async (i: number) => Math.abs((await box(bubble)).x + (await box(bubble)).width / 2 - ((await box(tiles.nth(i))).x + (await box(tiles.nth(i))).width / 2));
    await expect.poll(() => near(0)).toBeLessThan(2);
    const first = await colour();
    expect(await css(bubble, 'transition-property')).toContain('transform'); // a transition retargets from where it is
    await tiles.nth(3).hover();
    await tiles.nth(1).hover(); // turned back before it arrived
    await expect.poll(() => near(1)).toBeLessThan(2);
    await expect.poll(colour).not.toBe(first);
    await page.mouse.move(700, 880);
    await expect(bubble).toHaveCSS('opacity', '0');
    await tiles.nth(2).focus();
    await expect(bubble).toHaveCSS('opacity', '1'); // the keyboard gets the same
  });
});

test.describe('after the passphrase', () => {
  const mark = (page: Page) => page.locator('.donemark');
  const unlock = async (page: Page) => {
    await page.getByRole('button', { name: 'Elevate' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Elevate' }).click();
  };

  test('a wrong passphrase shakes and shows no success mark; the right one turns a ring into a check with the word', async ({ page }) => {
    await open(page, '#/projects/prj_mobile');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill('wrong one');
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Wrong passphrase.')).toBeVisible();
    // The shake is wide enough to be seen.
    const farthest = await dlg.locator('.pw').evaluate((e) => {
      const a = e.getAnimations().find((x) => (x as CSSAnimation).animationName === 'w-shake')!;
      a.pause();
      const d = a.effect!.getTiming().duration as number;
      let max = 0;
      for (let t = 0; t < d; t += 10) { a.currentTime = t; max = Math.max(max, Math.abs(new DOMMatrix(getComputedStyle(e).transform).m41)); }
      a.finish();
      return max;
    });
    expect(farthest).toBeGreaterThanOrEqual(10);
    await expect(mark(page)).toHaveCount(0);
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(mark(page).getByText('Approved', { exact: true })).toBeVisible();
    expect(await css(mark(page).locator('.spin'), 'animation-name')).toBe('w-spin');
    expect(await css(mark(page).locator('.tick'), 'animation-name')).toBe('w-draw');
    expect(await css(mark(page), 'pointer-events')).toBe('none'); // it never stands in the way
    await expect(mark(page)).toHaveCount(0, { timeout: 4000 }); // and leaves on its own
  });

  test('no mark while the request is still on its way', async ({ page }) => {
    await open(page, '#/projects/prj_mobile', '?latency=700');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByRole('button', { name: 'Working…' })).toBeVisible();
    expect(await mark(page).count()).toBe(0);
    await expect(mark(page).getByText('Approved')).toBeVisible({ timeout: 8000 });
  });

  test('a delay is never shown as success', async ({ page }) => {
    await open(page, '#/projects/prj_mobile', '?scenario=locked');
    await page.getByRole('button', { name: /^Approve m3-t2/ }).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByLabel('Approval passphrase').fill(PASS);
    await dlg.getByRole('button', { name: 'Approve' }).click();
    await expect(dlg.getByText('Too many attempts.')).toBeVisible();
    await expect(mark(page)).toHaveCount(0);
  });

  test('the review form and the unlock say what happened in their own word', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    await page.getByLabel(/Note for the agent/).fill('The build still fails.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Reject', exact: true }).click();
    await expect(mark(page).getByText('Rejected', { exact: true })).toBeVisible();
    await expect(mark(page)).toHaveClass(/bad/);
    await open(page, '#/projects/prj_mobile/plan');
    await unlock(page);
    await expect(mark(page).getByText('Elevated', { exact: true })).toBeVisible();
  });

  // Human, 2026-11-28: no card; the screen dims and the mark stands on it. A rejection ends in a cross that shakes.
  test('the screen dims behind the mark; approval is a check, rejection a cross that shakes', async ({ page }) => {
    await open(page, '#/projects/prj_docs/review');
    await page.getByLabel(/Override reason/).fill('Known issue.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Approve phase' }).click();
    await expect(mark(page).getByText('Approved', { exact: true })).toBeVisible();
    const screen = page.viewportSize()!;
    const dim = await box(mark(page));
    expect([dim.x, dim.y, dim.width, dim.height]).toEqual([0, 0, screen.width, screen.height]);
    expect(await css(mark(page), 'background-color')).toMatch(/^rgba\(.*, 0\.[5-9]\d*\)$/);
    const holder = mark(page).locator(':scope > div');
    expect(await css(holder, 'background-color')).toBe('rgba(0, 0, 0, 0)');
    expect(await css(holder, 'border-top-width')).toBe('0px');
    expect(await css(holder, 'box-shadow')).toBe('none');
    await expect(mark(page).locator('.cross')).toHaveCount(0);
    expect(await css(holder, 'animation-name')).not.toContain('w-shake');

    await open(page, '#/projects/prj_docs/review', '?scenario=default');
    await page.getByLabel(/Note for the agent/).fill('The build still fails.');
    await page.getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('button', { name: 'Reject', exact: true }).click();
    await expect(mark(page).getByText('Rejected', { exact: true })).toBeVisible();
    await expect(mark(page).locator('.cross')).toHaveCount(1);
    expect(await css(holder, 'animation-name')).toContain('w-shake');
    // The shake starts when the cross has been drawn.
    const delay = await holder.evaluate((e) => Number(e.getAnimations().find((x) => (x as CSSAnimation).animationName === 'w-shake')!.effect!.getTiming().delay));
    expect(delay).toBeGreaterThanOrEqual(1100);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    expect(await css(holder, 'animation-name')).toBe('none');
  });

  test('the mark never lies over the next prompt', async ({ page }) => {
    await open(page, '#/projects/prj_mobile/plan');
    await unlock(page);
    await expect(mark(page)).toHaveCount(1);
    await page.getByRole('button', { name: 'Approve', exact: true }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    expect(await mark(page).count()).toBe(0); // checked at once, long before it would leave on its own
  });

  test('the mark comes with the answer, not after the page has been read again', async ({ page }) => {
    await open(page, '#/activity', '?scenario=chain_broken&latency=1500');
    await page.getByRole('button', { name: 'Acknowledge the break' }).click();
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name: 'Acknowledge' }).click();
    await expect(page.getByRole('dialog')).toBeHidden({ timeout: 8000 });
    await expect(mark(page).getByText('Acknowledged')).toBeVisible({ timeout: 700 });
  });

  test('with reduced motion the check and the word stand still', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await open(page, '#/projects/prj_mobile/plan');
    await unlock(page);
    await expect(mark(page).getByText('Elevated')).toBeVisible();
    expect(await css(mark(page).locator('.tick'), 'animation-name')).toBe('none');
    expect(await css(mark(page).locator('.spin'), 'opacity')).toBe('0');
    await expect(mark(page)).toHaveCount(0, { timeout: 4000 });
  });
});

test.describe('the logo', () => {
  // Human, 2026-11-28: the browser tab shows the same mark as the page.
  test('the tab icon is drawn from the same paths and colours as the logo', async ({ page }) => {
    await open(page, '#/');
    const same = await page.evaluate(async () => {
      const res = await fetch(document.querySelector<HTMLLinkElement>('link[rel="icon"]')!.href);
      const icon = new DOMParser().parseFromString(await res.text(), 'image/svg+xml');
      const draw = (root: ParentNode, sel: string) => [...root.querySelectorAll(sel)].map((e) => e.getAttribute('d') ?? e.getAttribute('stop-color'));
      const logo = document.querySelector('aside.side .lg-mark')!;
      return { type: res.headers.get('content-type'), paths: [draw(icon, 'path'), draw(logo, 'path')], colours: [draw(icon, 'stop'), draw(logo, 'stop')] };
    });
    expect(same.type).toContain('image/svg+xml');
    expect(same.paths[0]!.length).toBe(3);
    expect(same.paths[0]).toEqual(same.paths[1]);
    expect(same.colours[0]).toEqual(same.colours[1]);
  });

  test('the rail carries the traced mark and shows the name when it opens', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/');
    const side = page.locator('aside.side');
    await expect(side.locator('.lg-mark svg')).toBeVisible();
    expect(await css(side.locator('.lg-word'), 'opacity')).toBe('0');
    await side.hover({ position: { x: 30, y: 300 } });
    await expect.poll(() => css(side.locator('.lg-word'), 'opacity')).toBe('1');
    await expect.poll(async () => (await box(side.locator('.lg-word'))).width).toBeGreaterThan(50);
  });
});

// Human, 2026-11-28: the task cards of a project are one size; a long title is cut and shown whole under the pointer.
test.describe('task cards', () => {
  const LONG = 'Rework the whole invoice reconciliation pipeline so that every refund, partial refund and chargeback is matched against the ledger';

  test('all cards are one size; a long title is cut, shown whole on hover and keyboard focus, and the card does not move', async ({ page }) => {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/projects/prj_billing');
    const cards = page.getByRole('list', { name: 'Tasks', exact: true }).getByRole('listitem');
    await expect(cards.first()).toBeVisible();
    const card = cards.first();
    const holder = card.locator('.ttl');
    await holder.evaluate((e, text) => { (e.querySelector('h3 a') ?? e.querySelector('h3'))!.textContent = text; e.querySelector('.full')!.textContent = text; }, LONG);
    await page.setViewportSize({ width: 1300, height: 900 }); // a new width: the cards measure their titles again
    await expect(holder).toHaveAttribute('data-cut', '');
    const heights = new Set<number>();
    for (const c of await cards.all()) heights.add(Math.round((await box(c)).height));
    expect(heights.size, `card heights ${[...heights]}`).toBe(1);

    const full = holder.locator('.full');
    await expect(full).toBeHidden();
    await holder.scrollIntoViewIfNeeded(); // so that the hover itself does not scroll the page between the two measures
    await page.waitForTimeout(500); // the cards have risen into place
    const before = await box(card);
    await holder.hover();
    await expect(full).toBeVisible();
    await expect(full).toHaveText(LONG);
    expect((await box(full)).height).toBeGreaterThan((await box(holder.locator('h3'))).height);
    expect(await box(card)).toEqual(before);
    expect(await css(card, 'z-index')).toBe('2'); // it lies over the cards below
    await page.mouse.move(640, 880);
    await expect(full).toBeHidden();
    // From the keyboard: the title itself, or its link, takes the focus.
    const link = holder.locator('a');
    await ((await link.count()) ? link : holder).focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    await expect(full).toBeVisible();

    // A title that fits shows nothing extra.
    const short = cards.nth(1).locator('.ttl');
    await expect(short).not.toHaveAttribute('data-cut', '');
    await page.getByRole('heading', { level: 1 }).click();
    await short.hover();
    await expect(short.locator('.full')).toBeHidden();
  });
});

// Human, 2026-11-28: an opening phase pushes what is in its way, and its frame widens ahead of it.
test.describe('opening a phase on the Graph', () => {
  type R = { x: number; y: number; w: number; h: number };
  // Boxes in the coordinates of the drawing, free of pan and zoom.
  const world = (page: Page) => page.evaluate(() => {
    const read = (e: Element) => { const h = e as HTMLElement; return { x: parseFloat(h.style.left), y: parseFloat(h.style.top), w: h.offsetWidth, h: h.offsetHeight }; };
    return {
      nodes: Object.fromEntries([...document.querySelectorAll('.node')].map((e) => [e.getAttribute('aria-label')!.split(',')[0] + ' @' + (e as HTMLElement).dataset.id, read(e)])),
      frames: [...document.querySelectorAll('.group')].map(read),
    };
  });
  const overlap = (a: R, b: R) => a.x < b.x + b.w - 1 && b.x < a.x + a.w - 1 && a.y < b.y + b.h - 1 && b.y < a.y + a.h - 1;
  const clash = (boxes: R[]) => boxes.flatMap((a, i) => boxes.slice(i + 1).filter((b) => overlap(a, b)).map((b) => [a, b]));
  const inside = (n: R, f: R) => n.x >= f.x - 1 && n.y >= f.y - 1 && n.x + n.w <= f.x + f.w + 1 && n.y + n.h <= f.y + f.h + 1;
  async function ready(page: Page) {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/graph');
    await expect(page.locator('.node .trow').first()).toBeAttached();
    await page.waitForTimeout(700);
  }
  // One press after the other, as a person would; `which` picks the buttons.
  const pressAll = async (page: Page, which: string) => {
    for (let b = page.locator(which); await b.count(); b = page.locator(which)) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
  };
  const closeAll = (page: Page) => pressAll(page, '.node .chev[aria-expanded="true"]');

  test('with every phase open, no phase lies on another, no frame on another, and every phase is inside its frame', async ({ page }) => {
    await ready(page);
    await closeAll(page);
    const rest = await world(page);
    await pressAll(page, '.node .chev[aria-expanded="false"]:not(:disabled)');
    const all = await world(page);
    expect(clash(Object.values(all.nodes))).toEqual([]);
    expect(clash(all.frames)).toEqual([]);
    for (const n of Object.values(all.nodes)) expect(all.frames.some((f) => inside(n, f)), JSON.stringify(n)).toBe(true);
    await closeAll(page);
    expect(await world(page)).toEqual(rest); // closing gives every phase its place back
  });

  test('a phase moved close by hand makes way while its neighbour is open and comes back afterwards', async ({ page }) => {
    await ready(page);
    await closeAll(page);
    const p2 = page.getByRole('group', { name: /^P2 Persistence layer/ });
    const p3 = page.getByRole('group', { name: /^P3 Payments API/ });
    const left = (l: Locator) => l.evaluate((e) => parseFloat((e as HTMLElement).style.left));
    await p3.focus();
    // Nudge P3 until it is 32 px to the right of P2 (24 is as close as two phases come).
    for (let i = 0; i < 40 && (await left(p3)) - (await left(p2)) - 144 > 32; i++) await page.keyboard.press('ArrowLeft');
    const near = await left(p3);
    expect(near - (await left(p2)) - 144).toBeLessThanOrEqual(32);
    await p2.getByRole('button', { name: /^Expand/ }).dispatchEvent('click');
    await expect.poll(async () => (await left(p3)) - (await left(p2)) - 264).toBeGreaterThanOrEqual(24);
    await p2.getByRole('button', { name: /^Collapse/ }).dispatchEvent('click');
    await expect.poll(() => left(p3)).toBe(near);
  });

  test('the frame widens ahead of the opening phase, which never leaves it, and the lines stay on the phases', async ({ page }) => {
    await ready(page);
    await closeAll(page);
    const seen = await page.evaluate(() => new Promise<Array<{ node: number; frame: number; out: number; gap: number }>>((done) => {
      const node = [...document.querySelectorAll<HTMLElement>('.node')].find((n) => n.getAttribute('aria-label')!.startsWith('P2 Persistence'))!;
      const frames = [...document.querySelectorAll<HTMLElement>('.group')];
      const frame = frames.find((f) => f.querySelector('b')!.textContent === 'billing-api')!;
      const from = { node: node.offsetHeight, frame: frame.offsetHeight };
      const out: Array<{ node: number; frame: number; out: number; gap: number }> = [];
      const start = performance.now();
      const step = () => {
        const ends = [...document.querySelectorAll('.edges path')].flatMap((p) => { const n = p.getAttribute('d')!.match(/-?[\d.]+/g)!.map(Number); return [[n[0]!, n[1]!]]; });
        // The line that leaves the right side of this phase starts exactly on that side.
        const right = node.offsetLeft + node.offsetWidth;
        const gap = Math.min(...ends.filter(([, y]) => Math.abs(y! - (node.offsetTop + 36)) < 8).map(([x]) => Math.abs(x! - right)));
        out.push({ node: node.offsetHeight - from.node, frame: frame.offsetHeight - from.frame, out: node.offsetTop + node.offsetHeight - (frame.offsetTop + frame.offsetHeight), gap });
        if (performance.now() - start < 800) requestAnimationFrame(step);
        else done(out);
      };
      node.querySelector<HTMLButtonElement>('.chev')!.click();
      requestAnimationFrame(step);
    }));
    const end = seen.at(-1)!;
    expect(end.node).toBeGreaterThan(60);
    const middle = seen.filter((s) => s.node > 5 && s.node < end.node - 5);
    expect(middle.length).toBeGreaterThan(5); // it glides
    for (const s of middle) expect(s.frame / end.frame).toBeGreaterThan(s.node / end.node); // the frame is ahead
    for (const s of seen) expect(s.out).toBeLessThanOrEqual(0); // never outside it
    for (const s of seen) expect(s.gap).toBeLessThan(6);
  });

  test('with reduced motion the phase and its frame take their size at once', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await ready(page);
    await closeAll(page);
    const p2 = page.getByRole('group', { name: /^P2 Persistence layer/ });
    await p2.getByRole('button', { name: /^Expand/ }).dispatchEvent('click');
    await page.waitForTimeout(50);
    expect((await p2.evaluate((e) => (e as HTMLElement).offsetWidth))).toBe(264);
  });
});

// Human, 2026-11-28: no frame lies on another, and a line meets its phases on the sides that face each other.
test.describe('arranging the Graph by hand', () => {
  type R = { x: number; y: number; w: number; h: number };
  const frames = (page: Page) => page.evaluate(() => Object.fromEntries([...document.querySelectorAll<HTMLElement>('.group')].map((e) => [e.querySelector('b')!.textContent!, { x: parseFloat(e.style.left), y: parseFloat(e.style.top), w: e.offsetWidth, h: e.offsetHeight }]))) as Promise<Record<string, R>>;
  const clash = (all: Record<string, R>) => Object.entries(all).flatMap(([a, p], i) => Object.entries(all).slice(i + 1).filter(([, q]) => p.x < q.x + q.w - 1 && q.x < p.x + p.w - 1 && p.y < q.y + q.h - 1 && q.y < p.y + p.h - 1).map(([b]) => `${a} / ${b}`));
  async function ready(page: Page) {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/graph');
    await expect(page.locator('.node .trow').first()).toBeAttached();
    for (let b = page.locator('.node .chev[aria-expanded="true"]'); await b.count(); ) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
  }
  async function drag(page: Page, from: Locator, dx: number, dy: number) {
    const b = await box(from);
    const x = b.x + Math.min(40, b.width / 2), y = b.y + 10;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + dx, y + dy, { steps: 25 });
    await page.mouse.up();
    await page.waitForTimeout(300);
  }

  test('a frame dragged onto another stops against it', async ({ page }) => {
    await ready(page);
    const before = await frames(page);
    const gap = before['billing-api-refund']!.y - (before['billing-api']!.y + before['billing-api']!.h);
    await drag(page, page.locator('.gtag b', { hasText: 'billing-api' }).first(), 0, gap + 60);
    const now = await frames(page);
    expect(clash(now)).toEqual([]);
    const left = now['billing-api-refund']!.y - (now['billing-api']!.y + now['billing-api']!.h);
    expect(left).toBeGreaterThanOrEqual(47); // room for the fork line stays
    expect(left).toBeLessThan(56); // and no more than that
    expect(now['billing-api-refund']).toEqual(before['billing-api-refund']); // the one in the way stays put
  });

  test('a phase that stretches its frame moves the frames below down, and stops at a frame above', async ({ page }) => {
    await ready(page);
    const before = await frames(page);
    await page.getByRole('group', { name: /^P1 Schema design/ }).focus();
    for (let i = 0; i < 6; i++) await page.keyboard.press('Shift+ArrowDown');
    let now = await frames(page);
    expect(now['billing-api']!.h).toBeGreaterThan(before['billing-api']!.h + 150);
    expect(now['billing-api-refund']!.y).toBeGreaterThan(before['billing-api-refund']!.y);
    expect(clash(now)).toEqual([]);
    for (let i = 0; i < 6; i++) await page.keyboard.press('Shift+ArrowUp');
    expect(await frames(page)).toEqual(before); // they come back
    // Upward there is a frame in the way: the phase stops against it.
    const mine = page.getByRole('group', { name: /^P1 Plan/ });
    await mine.focus();
    for (let i = 0; i < 12; i++) await page.keyboard.press('Shift+ArrowUp');
    now = await frames(page);
    expect(clash(now)).toEqual([]);
    expect(now['billing-api']).toEqual(before['billing-api']);
    expect(now['billing-api-refund']!.y).toBeGreaterThanOrEqual(now['billing-api']!.y + now['billing-api']!.h);
    await drag(page, mine, 0, -300);
    expect(clash(await frames(page))).toEqual([]);
  });

  test('a phase moved under its neighbour is joined top to bottom, and beside it again side to side', async ({ page }) => {
    await ready(page);
    const p3 = page.getByRole('group', { name: /^P3 Payments API/ });
    const p4 = page.getByRole('group', { name: /^P4 Hardening/ });
    const ends = () => page.evaluate(() => {
      const node = (name: string) => [...document.querySelectorAll<HTMLElement>('.node')].find((n) => n.getAttribute('aria-label')!.startsWith(name))!;
      const a = node('P3 Payments API'), b = node('P4 Hardening');
      const on = (n: HTMLElement, x: number, y: number) => {
        const l = n.offsetLeft, t = n.offsetTop, r = l + n.offsetWidth, btm = t + n.offsetHeight, at = (u: number, v: number) => Math.abs(u - v) < 1.5;
        return at(x, r) ? 'right' : at(x, l) ? 'left' : at(y, t) ? 'top' : at(y, btm) ? 'bottom' : '';
      };
      return [...document.querySelectorAll('.edges path:not(.fork)')].map((p) => p.getAttribute('d')!.match(/-?[\d.]+/g)!.map(Number))
        .map((n) => [on(a, n[0]!, n[1]!), on(b, n[6]!, n[7]!)]).find(([u, v]) => u && v);
    });
    expect(await ends()).toEqual(['right', 'left']);
    const [a, b] = [await box(p3), await box(p4)];
    await drag(page, p4, a.x - b.x, a.height + 50);
    expect(await ends()).toEqual(['bottom', 'top']);
    const moved = await box(p4);
    await drag(page, p4, b.x - moved.x, b.y - moved.y);
    expect(await ends()).toEqual(['right', 'left']);
  });

  // Human, 2026-11-28: the fork line between two frames follows them too.
  test('the fork line joins the sides of the two frames that face each other', async ({ page }) => {
    await ready(page);
    const fork = () => page.evaluate(() => {
      const frame = (name: string) => [...document.querySelectorAll<HTMLElement>('.group')].find((f) => f.querySelector('b')!.textContent === name)!;
      const on = (f: HTMLElement, x: number, y: number) => {
        const l = f.offsetLeft, t = f.offsetTop, r = l + f.offsetWidth, b = t + f.offsetHeight, at = (u: number, v: number) => Math.abs(u - v) < 1.5;
        return at(x, r) ? 'right' : at(x, l) ? 'left' : at(y, t) ? 'top' : at(y, b) ? 'bottom' : '';
      };
      const n = document.querySelector('.edges path.fork')!.getAttribute('d')!.match(/-?[\d.]+/g)!.map(Number);
      return [on(frame('billing-api'), n[0]!, n[1]!), on(frame('billing-api-refund'), n[6]!, n[7]!), document.querySelector('.elabel')!.textContent!.trim()];
    });
    expect(await fork()).toEqual(['bottom', 'top', 'fork of billing-api']);
    // Carry the fork's frame to the right of its parent, then up beside it.
    const before = await frames(page);
    const tag = await box(page.locator('.gtag b', { hasText: 'billing-api-refund' }));
    const x = tag.x + 30, y = tag.y + 10, right = before['billing-api']!.w + 70;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + right, y, { steps: 30 });
    await page.mouse.move(x + right, y - (before['billing-api-refund']!.y - before['billing-api']!.y), { steps: 30 });
    await page.mouse.up();
    await page.waitForTimeout(300);
    const now = await frames(page);
    expect(clash(now)).toEqual([]);
    expect(now['billing-api-refund']!.x).toBeGreaterThan(now['billing-api']!.x + now['billing-api']!.w);
    expect(await fork()).toEqual(['right', 'left', 'fork']); // close together: the short word fits between them
  });

  // Review finding, 2026-11-28: only where the hand lets go counts.
  test('a picked side survives a drag that passes another way and comes back before letting go', async ({ page }) => {
    await ready(page);
    await page.getByRole('button', { name: 'Tools', exact: true }).click();
    await page.getByText(/^Line sides \(/).click();
    const start = page.getByLabel('Start side, line P3 to P4 in billing-api', { exact: true });
    await start.selectOption('top');
    await page.getByRole('button', { name: 'Tools', exact: true }).click();
    await page.waitForTimeout(400);
    const p4 = await box(page.getByRole('group', { name: /^P4 Hardening/ }));
    const p3 = await box(page.getByRole('group', { name: /^P3 Payments API/ }));
    await page.mouse.move(p4.x + 40, p4.y + 12);
    await page.mouse.down();
    await page.mouse.move(p3.x + 40, p3.y + p3.height + 70, { steps: 20 });
    const under = await box(page.getByRole('group', { name: /^P4 Hardening/ }));
    expect(under.y).toBeGreaterThan(p3.y + p3.height + 24); // it really is under P3 now
    expect(Math.abs(under.x - p3.x)).toBeLessThan(40);
    await page.mouse.move(p4.x + 40, p4.y + 12, { steps: 20 });
    await page.mouse.up();
    await page.getByRole('button', { name: 'Tools', exact: true }).click();
    await expect(page.getByLabel('Start side, line P3 to P4 in billing-api', { exact: true })).toHaveValue('top');
  });

  test('a side picked by hand holds while the phases face the same way, and gives way when they no longer do', async ({ page }) => {
    await ready(page);
    await page.getByRole('button', { name: 'Tools', exact: true }).click();
    await page.getByText(/^Line sides \(/).click();
    const start = page.getByLabel('Start side, line P3 to P4 in billing-api', { exact: true });
    const end = page.getByLabel('End side, line P3 to P4 in billing-api', { exact: true });
    await start.selectOption('top');
    await expect(end).toHaveValue('left'); // the other end keeps the side it had
    const p4 = page.getByRole('group', { name: /^P4 Hardening/ });
    await p4.focus();
    await page.keyboard.press('ArrowRight');
    await expect(start).toHaveValue('top'); // a nudge changes nothing about who faces whom
    for (let i = 0; i < 9; i++) await page.keyboard.press('Shift+ArrowDown');
    await expect(start).toHaveValue('bottom');
    await expect(end).toHaveValue('top');
  });
});

// Human, 2026-11-28: phases never lie on each other; the one that is approached steps aside.
test.describe('phases step aside', () => {
  type R = { name: string; x: number; y: number; w: number; h: number };
  const nodes = (page: Page) => page.evaluate(() => [...document.querySelectorAll<HTMLElement>('.node')].map((e) => ({ name: e.getAttribute('aria-label')!.split(',')[0]!, x: parseFloat(e.style.left), y: parseFloat(e.style.top), w: e.offsetWidth, h: e.offsetHeight })));
  const tooClose = (list: R[]) => list.flatMap((a, i) => list.slice(i + 1).filter((b) => a.x < b.x + b.w + 23 && b.x < a.x + a.w + 23 && a.y < b.y + b.h + 23 && b.y < a.y + a.h + 23).map((b) => `${a.name} / ${b.name}`));
  const find = (list: R[], name: string) => list.find((n) => n.name.startsWith(name))!;
  async function ready(page: Page) {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/graph');
    await expect(page.locator('.node .trow').first()).toBeAttached();
    for (let b = page.locator('.node .chev[aria-expanded="true"]'); await b.count(); ) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
  }

  test('a phase carried onto another makes it step aside, and it stays aside only if the carried one stays', async ({ page }) => {
    await ready(page);
    const start = await nodes(page);
    const p2 = await box(page.getByRole('group', { name: /^P2 Persistence layer/ }));
    const p3 = await box(page.getByRole('group', { name: /^P3 Payments API/ }));
    const x = p2.x + 40, y = p2.y + 12;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + (p3.x - p2.x) - 60, y, { steps: 20 }); // half onto P3
    await page.waitForTimeout(400);
    let now = await nodes(page);
    expect(tooClose(now)).toEqual([]);
    expect(find(now, 'P3 Payments')).not.toEqual(find(start, 'P3 Payments')); // it stepped aside
    await page.mouse.move(x, y, { steps: 20 }); // and back again
    await page.mouse.up();
    await page.waitForTimeout(400);
    expect(await nodes(page)).toEqual(start); // nothing was disturbed for good

    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + (p3.x - p2.x) - 60, y, { steps: 20 });
    await page.mouse.up(); // this time it stays
    await page.waitForTimeout(400);
    now = await nodes(page);
    expect(tooClose(now)).toEqual([]);
    const aside = find(now, 'P3 Payments');
    expect(aside).not.toEqual(find(start, 'P3 Payments'));
    await page.evaluate(() => (location.hash = '/projects')); // the new places are saved
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await page.evaluate(() => (location.hash = '/graph'));
    await expect(page.locator('.node').first()).toBeVisible();
    for (let b = page.locator('.node .chev[aria-expanded="true"]'); await b.count(); ) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
    expect(find(await nodes(page), 'P3 Payments')).toEqual(aside);
  });

  test('the same from the keyboard, with an open phase too, and no frame is pushed onto another', async ({ page }) => {
    await ready(page);
    await page.getByRole('group', { name: /^P2 Persistence layer/ }).getByRole('button', { name: /^Expand/ }).dispatchEvent('click');
    await page.waitForTimeout(900);
    const p1 = page.getByRole('group', { name: /^P1 Schema design/ });
    await p1.focus();
    for (let i = 0; i < 10; i++) {
      await page.keyboard.press('Shift+ArrowRight');
      expect(tooClose(await nodes(page))).toEqual([]);
    }
    const frames = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('.group')].map((e) => ({ x: parseFloat(e.style.left), y: parseFloat(e.style.top), w: e.offsetWidth, h: e.offsetHeight })));
    for (const [i, a] of frames.entries()) for (const b of frames.slice(i + 1)) expect(a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h).toBe(false);
  });
});

// A guard change lands on the project the human was shown, whatever happens to the active project meanwhile: another client
// with the operator token can change that while the passphrase is being typed.
test.describe('guard dialogs on Harnesses', () => {
  const elsewhere = async (page: Page, id: string) => {
    const status = await page.evaluate((i) => (window as unknown as { __wavesMock: { activate(id: string): Promise<{ status: number }> } }).__wavesMock.activate(i), id);
    expect(status.status).toBe(200);
    await page.waitForTimeout(800); // the change event, the coalescing delay, the new read
  };
  const confirm = async (page: Page, name: string) => {
    await page.getByRole('dialog').getByLabel('Approval passphrase').fill(PASS);
    await page.getByRole('dialog').getByRole('button', { name }).click();
    await expect(page.getByRole('dialog')).toBeHidden();
  };
  const panel = (page: Page, project: string) => page.getByRole('region', { name: `Guard policy · ${project}` });

  test('all three dialogs name their project and change that one, also when the active project moves while they are open', async ({ page }) => {
    await open(page, '#/harnesses');
    await expect(panel(page, 'billing-api')).toBeVisible();
    const dlg = page.getByRole('dialog');

    await page.getByRole('button', { name: 'Change policy' }).click();
    await expect(dlg.getByText('billing-api', { exact: true })).toBeVisible();
    await elsewhere(page, 'prj_docs');
    await expect(dlg.getByText('billing-api', { exact: true })).toBeVisible();
    await dlg.getByRole('radio', { name: /^enforce/ }).check();
    await confirm(page, 'Save');
    await expect(panel(page, 'billing-api')).toContainText('Current policy: enforce.'); // the page stayed, and the change is here

    await page.getByRole('button', { name: 'Change', exact: true }).click();
    await expect(dlg.getByText('billing-api', { exact: true })).toBeVisible();
    await elsewhere(page, 'prj_mobile');
    await dlg.getByRole('radio', { name: 'Deny' }).check();
    await confirm(page, 'Save');
    await expect(page.getByText(/never claimed a task is currently: denied/)).toBeVisible();

    await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
    await expect(dlg.getByText('billing-api', { exact: true })).toBeVisible();
    await elsewhere(page, 'prj_docs');
    await confirm(page, 'Disconnect');
    await expect(panel(page, 'billing-api')).toContainText('Current policy: off.');
    await expect(page.getByText('Not connected to billing-api')).toBeVisible();

    await page.getByLabel('Project').selectOption({ label: 'docs-site' });
    await expect(panel(page, 'docs-site')).toBeVisible();
    await expect(panel(page, 'docs-site')).not.toContainText('Current policy: enforce.'); // nothing of it reached the other one
  });

  test('another project\'s guard is never offered for change while the chosen one is still being read', async ({ page }) => {
    await open(page, '#/harnesses', '?latency=700');
    await expect(page.getByRole('button', { name: 'Change policy' })).toBeVisible();
    await page.getByLabel('Project').selectOption({ label: 'docs-site' });
    expect(await page.getByRole('button', { name: 'Change policy' }).count()).toBe(0); // checked at once
    await expect(panel(page, 'docs-site')).toBeVisible();
  });
});

// Review findings, 2026-11-28: a phase in a project that was pushed aside goes where the hand puts it, and only that is saved.
test.describe('moving a phase in a project that was pushed aside', () => {
  type At = { x: number; y: number };
  const at = (page: Page, name: RegExp) => page.getByRole('group', { name }).evaluate((e) => ({ x: parseFloat((e as HTMLElement).style.left), y: parseFloat((e as HTMLElement).style.top) })) as Promise<At>;
  const again = async (page: Page) => {
    await page.evaluate(() => (location.hash = '/projects'));
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await page.evaluate(() => (location.hash = '/graph'));
    await expect(page.locator('.node').first()).toBeVisible();
    await page.waitForTimeout(900);
  };
  async function ready(page: Page) {
    await page.setViewportSize({ width: 1360, height: 900 });
    await open(page, '#/graph');
    await expect(page.locator('.node .trow').first()).toBeAttached();
    await page.waitForTimeout(900);
  }
  const [FIRST, SECOND] = [/^P1 Plan/, /^P2 Refund endpoint/];

  test('pushed by an opened phase: the phase goes exactly as far as it is moved, its neighbour stays, and closing leaves them level', async ({ page }) => {
    await ready(page); // billing-api P2 is open, so the project below is pushed down
    const [a, b] = [await at(page, FIRST), await at(page, SECOND)];
    await page.getByRole('group', { name: FIRST }).focus();
    await page.keyboard.press('ArrowRight');
    expect(await at(page, FIRST)).toEqual({ x: a.x + 8, y: a.y });
    expect(await at(page, SECOND)).toEqual(b);
    await page.keyboard.press('ArrowLeft');
    await page.waitForTimeout(400);
    const held = await box(page.getByRole('group', { name: FIRST }));
    await page.mouse.move(held.x + 40, held.y + 12);
    await page.mouse.down();
    await page.mouse.move(held.x + 20, held.y + 12, { steps: 5 });
    await page.mouse.up();
    expect(await at(page, FIRST)).toEqual({ x: a.x - 20, y: a.y });
    expect(await at(page, SECOND)).toEqual(b);
    await page.getByRole('group', { name: /^P2 Persistence layer/ }).getByRole('button', { name: /^Collapse/ }).dispatchEvent('click');
    await page.waitForTimeout(900);
    expect((await at(page, FIRST)).y).toBe((await at(page, SECOND)).y);
  });

  test('pushed by a stretched project above: nudging does not run away, and the places survive a new visit', async ({ page }) => {
    await ready(page);
    for (let b = page.locator('.node .chev[aria-expanded="true"]'); await b.count(); ) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
    await page.getByRole('group', { name: /^P1 Schema design/ }).focus();
    for (let i = 0; i < 6; i++) await page.keyboard.press('Shift+ArrowDown');
    const [a, b] = [await at(page, FIRST), await at(page, SECOND)];
    await page.getByRole('group', { name: FIRST }).focus();
    for (let i = 0; i < 3; i++) await page.keyboard.press('ArrowUp'); // there is a project in the way
    await page.keyboard.press('ArrowRight');
    const [c, d] = [await at(page, FIRST), await at(page, SECOND)];
    expect(c).toEqual({ x: a.x + 8, y: a.y });
    expect(d).toEqual(b);
    await again(page);
    for (let b = page.locator('.node .chev[aria-expanded="true"]'); await b.count(); ) await b.first().dispatchEvent('click');
    await page.waitForTimeout(900);
    expect(await at(page, FIRST)).toEqual(c);
    expect(await at(page, SECOND)).toEqual(d);
  });
});

test('an arrangement that could not be read at first is taken in when it can, and is not written over', async ({ page }) => {
  await page.setViewportSize({ width: 1360, height: 900 });
  await open(page, '#/graph', '?scenario=layout_flaky');
  await expect(page.getByText('The saved arrangement could not be read.')).toBeVisible();
  const left = () => page.getByRole('group', { name: /^P4 Hardening/ }).evaluate((e) => (e as HTMLElement).style.left);
  await page.evaluate(() => (window as unknown as { __wavesMock: { drop(): void } }).__wavesMock.drop()); // the stream returns and everything is read again
  await expect(page.getByText('The saved arrangement could not be read.')).toHaveCount(0, { timeout: 10_000 });
  await expect.poll(left).toBe('900px');
  await page.getByRole('group', { name: /^P1 Content audit/ }).focus();
  await page.keyboard.press('ArrowRight'); // this saves the arrangement
  await page.evaluate(() => (location.hash = '/projects'));
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
  await page.evaluate(() => (location.hash = '/graph'));
  await expect.poll(left).toBe('900px');
});

test('a task title of the greatest length is shown whole on a phone and on a wide screen', async ({ page }) => {
  for (const width of [390, 1360]) {
    await page.setViewportSize({ width, height: 900 });
    await open(page, '#/projects/prj_billing');
    const card = page.getByRole('list', { name: 'Tasks', exact: true }).getByRole('listitem').first();
    await expect(card).toBeVisible();
    expect(await css(card, 'overflow')).toBe('visible'); // nothing cuts what lies on top of the card
  }
});

test('a disabled button does not look like one that can be pressed', async ({ page }) => {
  await open(page, '#/projects/prj_docs/review');
  const approve = page.getByRole('button', { name: 'Approve phase' });
  await expect(approve).toBeDisabled();
  expect(await css(approve, 'border-top-style')).toBe('dashed');
});
