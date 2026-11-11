import { describe, expect, it } from 'vitest';
import { ApiError, createClient } from './client';
import type { Transport, TransportRequest } from './transport';

function recorder(res: { status: number; text: string } | Error) {
  const seen: TransportRequest[] = [];
  const t: Transport = async (req) => {
    seen.push(req);
    if (res instanceof Error) throw res;
    return res;
  };
  return { seen, t };
}

describe('client', () => {
  it('sends the CSRF header on writes only', async () => {
    const { seen, t } = recorder({ status: 200, text: '{"version":"x"}' });
    const c = createClient(t, { csrf: 'tok', onUnauthenticated() {} });
    await c.health();
    await c.backup();
    expect(seen[0]!.headers['X-Waves-CSRF']).toBeUndefined();
    expect(seen[1]!.headers['X-Waves-CSRF']).toBe('tok');
  });

  it('puts path parameters and the query in the URL, encoded', async () => {
    const { seen, t } = recorder({ status: 200, text: '' });
    const c = createClient(t, { csrf: null, onUnauthenticated() {} });
    await c.log('exe a/b', 12);
    expect(seen[0]!.url).toBe('/executions/exe%20a%2Fb/log?offset=12');
  });

  it('turns an error body into an ApiError with the next step', async () => {
    const { t } = recorder({ status: 403, text: JSON.stringify({ error: { code: 'stepup_required', message: 'Needs passphrase.', next_step: 'Confirm.' } }) });
    const c = createClient(t, { csrf: null, onUnauthenticated() {} });
    const e = await c.backup().catch((x) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect(e).toMatchObject({ status: 403, code: 'stepup_required', message: 'Needs passphrase.', nextStep: 'Confirm.' });
  });

  it('reports a 401 outside /auth as an ended session, but not inside /auth', async () => {
    let n = 0;
    const { t } = recorder({ status: 401, text: JSON.stringify({ error: { code: 'unauthenticated', message: 'x' } }) });
    const c = createClient(t, { csrf: null, onUnauthenticated: () => n++ });
    await c.status().catch(() => {});
    await c.consumeLink('abc').catch(() => {});
    expect(n).toBe(1);
  });

  it('survives a non-JSON error and a dead service', async () => {
    const c1 = createClient(recorder({ status: 502, text: '<html>bad gateway</html>' }).t, { csrf: null, onUnauthenticated() {} });
    expect(await c1.status().catch((x) => x)).toMatchObject({ status: 502, code: 'http_502' });
    const c2 = createClient(recorder(new Error('down')).t, { csrf: null, onUnauthenticated() {} });
    expect(await c2.status().catch((x) => x)).toMatchObject({ status: 0, code: 'unreachable' });
  });

  it('sends the elevation secret only when given', async () => {
    const { seen, t } = recorder({ status: 200, text: '{}' });
    const c = createClient(t, { csrf: 'x', onUnauthenticated() {} });
    await c.decide('apr_1', { decision: 'reject', subject_hash: 'h', feedback: 'no' });
    await c.decide('apr_1', { decision: 'approve', subject_hash: 'h' }, 'secret-value');
    expect(seen[0]!.headers['X-Waves-Elevation']).toBeUndefined();
    expect(seen[1]!.headers['X-Waves-Elevation']).toBe('secret-value');
  });
});
