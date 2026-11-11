// Gate-bypass negative tests for the mock service. The real service must pass the
// same cases; here they keep the mock honest, so the UI never learns
// a shortcut that will not exist.
import { describe, expect, it } from 'vitest';
import { MOCK_PASSPHRASE, setup } from '../test-helpers';
import { MOCK_CSRF } from './seed';
import { createClient } from '../api/client';

const code = async (p: Promise<unknown>) => ((await p.then(() => ({ code: 'ok' }), (e) => e)) as { code: string }).code;
const approve = (api: ReturnType<typeof setup>['api'], id: string, hash: string, secret?: string, extra: object = {}) =>
  api.decide(id, { decision: 'approve', subject_hash: hash, ...extra }, secret);

describe('approval gates', () => {
  it('refuses to approve without an elevation', async () => {
    const { api } = setup();
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1'))).toBe('stepup_required');
  });

  it('refuses a made-up or empty elevation secret', async () => {
    const { api } = setup();
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', 'elv_forged'))).toBe('stepup_required');
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', ''))).toBe('stepup_required');
  });

  it('a time window never covers a phase gate', async () => {
    const { api } = setup();
    const w = await api.stepUp(MOCK_PASSPHRASE);
    expect(w.kind).toBe('window');
    expect(await code(approve(api, 'apr_d1done', 'sh_d1_a41c7e09', w.elevation_secret, { override_reason: 'x' }))).toBe('stepup_required');
  });

  it('a single-use elevation is bound to its approval and cannot be replayed', async () => {
    const { api } = setup();
    const e = await api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2');
    expect(await code(approve(api, 'apr_mwf2', 'sh_mwf2_7d20c4be', e.elevation_secret))).toBe('stepup_required'); // other approval
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', e.elevation_secret))).toBe('ok');
    expect(await code(approve(api, 'apr_mwf2', 'sh_mwf2_7d20c4be', e.elevation_secret))).toBe('stepup_required'); // spent
  });

  it('a phase completion needs its own fresh step-up and a reason when a validator failed', async () => {
    const { api } = setup();
    const e = await api.stepUp(MOCK_PASSPHRASE, 'apr_d1done');
    expect(await code(approve(api, 'apr_d1done', 'sh_d1_a41c7e09', e.elevation_secret))).toBe('validation_failed');
    expect(await code(approve(api, 'apr_d1done', 'sh_d1_a41c7e09', e.elevation_secret, { override_reason: 'Front matter fix is tracked.' }))).toBe('ok');
    expect((await api.approval('apr_d1done')).state).toBe('approved');
  });

  it('rejects a stale subject hash', async () => {
    const { api } = setup();
    const e = await api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2');
    expect(await code(approve(api, 'apr_m3t2', 'sh_changed', e.elevation_secret))).toBe('stale_subject');
  });

  it('an operator can reject a result but not a phase gate without an elevation', async () => {
    const { api } = setup();
    expect(await code(api.decide('apr_m3t2', { decision: 'reject', subject_hash: 'x', feedback: 'redo' }))).toBe('ok');
    expect(await code(api.decide('apr_d1done', { decision: 'reject', subject_hash: 'x', feedback: 'redo' }))).toBe('stepup_required');
  });

  it('decides an approval only once', async () => {
    const { api } = setup();
    const e = await api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2');
    await approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', e.elevation_secret);
    const again = await api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2');
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', again.elevation_secret))).toBe('invalid_state');
  });
});

describe('step-up', () => {
  it('counts wrong passphrases, then rate limits, even for the right one', async () => {
    const { api } = setup();
    const e1 = await api.stepUp('nope').catch((x) => x);
    expect(e1).toMatchObject({ code: 'stepup_failed' });
    expect(e1.body.attempts_left).toBe(2);
    await api.stepUp('nope').catch(() => {});
    const e3 = await api.stepUp('nope').catch((x) => x);
    expect(e3).toMatchObject({ code: 'rate_limited' });
    expect(await code(api.stepUp(MOCK_PASSPHRASE))).toBe('rate_limited');
  });

  it('is blocked while the audit chain is broken, and approval too', async () => {
    const { api } = setup({ scenario: 'chain_broken' });
    expect(await code(api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2'))).toBe('audit_chain_broken');
    expect(await code(approve(api, 'apr_m3t2', 'sh_m3t2_b220f1', 'whatever'))).toBe('audit_chain_broken');
  });

  it('is unavailable until the passphrase is set', async () => {
    const { api } = setup({ scenario: 'first_run' });
    expect(await code(api.stepUp(MOCK_PASSPHRASE))).toBe('passphrase_not_set');
  });
});

describe('session and settings', () => {
  it('refuses a write without the CSRF token', async () => {
    const { mock } = setup();
    const api = createClient(mock.transport, { csrf: null, onUnauthenticated() {} });
    expect(await code(api.backup())).toBe('csrf_failed');
    void MOCK_CSRF;
  });

  it('answers 401 when signed out and lets a valid link sign in once', async () => {
    const { api, session } = setup({ scenario: 'signed_out' });
    expect(await code(api.status())).toBe('unauthenticated');
    expect(session.unauth).toBe(1);
    expect(await code(api.consumeLink('wrong-code'))).toBe('unauthenticated');
    expect(await code(api.consumeLink('link-valid-demo'))).toBe('ok');
    expect(await code(api.status())).toBe('ok');
  });

  it('security settings need an elevation, plain ones do not', async () => {
    const { api } = setup();
    expect(await code(api.patchSettings({ max_concurrent: 4 }))).toBe('stepup_required');
    expect(await code(api.patchSettings({ heartbeat_interval_s: 30 }))).toBe('ok');
    expect(await code(api.patchSettings({ heartbeat_interval_s: 5 }))).toBe('validation_failed');
    const w = await api.stepUp(MOCK_PASSPHRASE);
    expect(await code(api.patchSettings({ max_concurrent: 4 }, w.elevation_secret))).toBe('ok');
    expect(await code(api.patchSettings({ max_concurrent: 99 }, w.elevation_secret))).toBe('validation_failed');
  });

  it('guard policy and connection need an elevation', async () => {
    const { api } = setup();
    expect(await code(api.patchProjectSettings('prj_billing', { guard_policy: 'enforce' }))).toBe('stepup_required');
    const plan = await api.connectPlan('prj_docs'); // the dry run is an operator route and changes nothing
    expect(plan.applied).toBe(false);
    expect(await code(api.connect('prj_docs', plan.plan_hash))).toBe('stepup_required');
    const w = await api.stepUp(MOCK_PASSPHRASE);
    expect(await code(api.connect('prj_docs', 'ph_other', w.elevation_secret))).toBe('stale_subject'); // not the plan that was reviewed
    expect(await code(api.connect('prj_docs', plan.plan_hash, w.elevation_secret))).toBe('ok');
  });

  it('refuses settings keys it does not know, so hard caps, listen and LAN stay out of reach', async () => {
    const { api } = setup();
    const w = await api.stepUp(MOCK_PASSPHRASE);
    for (const body of [{ hard_cap: 99 }, { lan_enabled: true }, { listen: '0.0.0.0:7421' }, { passphrase_set: false }]) {
      expect(await code(api.patchSettings(body as never, w.elevation_secret))).toBe('validation_failed');
    }
    expect((await api.settings()).hard_cap).toBe(8);
  });
});

describe('elevation lifetime', () => {
  it('lock again revokes the confirmation at the service, not only in the browser', async () => {
    const { api } = setup();
    const w = await api.stepUp(MOCK_PASSPHRASE);
    expect(await code(api.patchSettings({ max_concurrent: 3 }, w.elevation_secret))).toBe('ok');
    await api.revokeElevation(w.elevation_secret);
    expect(await code(api.patchSettings({ max_concurrent: 4 }, w.elevation_secret))).toBe('stepup_required');
  });

  it('a broken audit chain blocks decisions but still lets a human step up and acknowledge it', async () => {
    const { api } = setup({ scenario: 'chain_broken' });
    expect(await code(api.stepUp(MOCK_PASSPHRASE, 'apr_m3t2'))).toBe('audit_chain_broken');
    const w = await api.stepUp(MOCK_PASSPHRASE); // a window, not bound to an approval
    expect(await code(api.decide('apr_m3t2', { decision: 'approve', subject_hash: 'sh_m3t2_b220f1' }, w.elevation_secret))).toBe('audit_chain_broken');
    expect(await code(api.acknowledgeAudit(w.elevation_secret))).toBe('ok');
    expect((await api.audit()).status.state).toBe('verified');
  });

  it('refuses an address that would walk out of its route', async () => {
    const { api } = setup();
    expect(await code(api.execution('..'))).toBe('invalid_id');
    expect(await code(api.project('.'))).toBe('invalid_id');
  });
});
