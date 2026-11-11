import { test, expect, type Page } from '@playwright/test';

// Walks every page the dashboard links to on a real service with real data, and checks that
// each one loads cleanly: no script error, no failed API call, one h1, no "could not load" state.
// WAVES_SHOTS (optional) is a directory that receives one screenshot per page.
const OP = process.env.WAVES_OPERATOR_TOKEN ?? '';
const SHOTS = process.env.WAVES_SHOTS;
const SKIP = /^#\/(signin|signed-out|welcome)/;

async function signIn(page: Page) {
  const res = await page.request.post('/api/v1/auth/login-links', { headers: { Authorization: `Bearer ${OP}` } });
  expect(res.status()).toBe(201);
  await page.goto((await res.json()).url);
  await page.getByRole('button', { name: 'Open dashboard' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
}

test('every reachable page loads with real data and no errors', async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const problems: string[] = [];
  let at = '';
  page.on('pageerror', (e) => problems.push(`${at}: script error ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') problems.push(`${at}: console ${m.text()}`); });
  page.on('response', (r) => { if (r.url().includes('/api/') && r.status() >= 400) problems.push(`${at}: ${r.status()} ${r.request().method()} ${new URL(r.url()).pathname}`); });

  const queue = ['#/', '#/projects', '#/approvals', '#/executions', '#/harnesses', '#/activity', '#/settings'];
  const seen = new Set<string>();
  while (queue.length && seen.size < 60) {
    const h = queue.shift()!;
    if (seen.has(h)) continue;
    seen.add(h);
    at = h;
    await page.goto('/' + h);
    await expect(page.locator('main h1')).toHaveCount(1);
    await expect(page.getByRole('status', { name: /^Loading/ })).toHaveCount(0, { timeout: 10_000 });
    await page.waitForTimeout(250); // let secondary resources settle
    const body = (await page.locator('main').innerText()).replace(/\s+/g, ' ');
    for (const bad of ['Could not load', 'Showing older data', 'undefined', 'NaN', '[object Object]']) if (body.includes(bad)) problems.push(`${h}: page says "${bad}"`);
    const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    if (over > 0) problems.push(`${h}: scrolls sideways by ${over}px`);
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/${h.replace(/[^a-z0-9]+/gi, '_') || 'home'}.png`, fullPage: true });
    for (const l of await page.locator('main a[href^="#/"]').evaluateAll((as) => as.map((a) => a.getAttribute('href')!))) if (!SKIP.test(l) && !seen.has(l)) queue.push(l);
  }
  console.log(`visited ${seen.size} pages:\n${[...seen].join('\n')}`);
  expect(problems).toEqual([]);
  expect(seen.size).toBeGreaterThan(12);
  // The pages most likely to break must really have been walked, not just exist as links.
  const walked = [...seen];
  for (const [name, re] of [['phase review', /^#\/phases\/[^/]+\/review$/], ['workflow review', /^#\/workflow\//], ['requirements', /^#\/projects\/[^/]+\/requirements$/], ['plan', /^#\/projects\/[^/]+\/plan$/], ['recovery', /^#\/executions\/[^/]+\/recovery$/], ['approval detail', /^#\/approvals\/[^/]+$/]] as const) {
    expect(walked.some((h) => re.test(h)), `the sweep never reached ${name}`).toBe(true);
  }
});
