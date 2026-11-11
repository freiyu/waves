// The mock adapter: an in-memory stand-in for the Core Service. It sits behind the
// same Transport the real client uses, so the client, CSRF, elevation and error
// handling all run for real. It follows the contract and enforces the same
// gates the real service will: no approval without a step-up.
import type { Transport, TransportRequest, TransportResponse } from '../api/transport';
import type { ApprovalDetail, AttentionItem, ExecutionDetail, Status } from '../api/models';
import { FIRST_VALID_LINK, MOCK_CSRF, MOCK_PASSPHRASE, seed, type MockDb } from './seed';
import { MockBus } from './bus';

export interface MockOptions {
  now?: number;
  /** URL scenario: empty, error, slow, chain_broken, reset, drift, first_run, signed_out, locked. */
  scenario?: string;
  latencyMs?: number;
  /** Milliseconds between log lines of the running execution, as a live agent would write them. 0 or absent: none. */
  tickMs?: number;
}

interface Ctx { params: Record<string, string>; query: URLSearchParams; body: any; headers: Record<string, string>; db: MockDb; bus: MockBus }
type Out = { status?: number; json?: unknown; text?: string };
type Handler = (c: Ctx) => Out;

class Fail extends Error {
  constructor(public status: number, public code: string, message: string, public next = '', public extra: Record<string, unknown> = {}) {
    super(message);
  }
}
const fail = (status: number, code: string, message: string, next = '', extra: Record<string, unknown> = {}) => new Fail(status, code, message, next, extra);
const ok = (json: unknown, status = 200): Out => ({ status, json });
const none = (): Out => ({ status: 204 });

const iso = (ms: number) => new Date(ms).toISOString();

export function createMock(opts: MockOptions = {}) {
  const db = seed(opts.now);
  const bus = new MockBus();
  const scenario = opts.scenario ?? '';
  const now = () => Date.now();
  if (scenario === 'chain_broken') db.chainBroken = true;
  if (scenario === 'reset') db.resetAlert = true;
  if (scenario === 'first_run') db.passphraseSet = false, (db.settings.passphrase_set = false);
  if (scenario === 'signed_out') db.signedIn = false;
  if (scenario === 'locked') db.lockedUntil = now() + 252_000;
  let counter = 0;
  const secret = (p: string) => `${p}_${(++counter).toString(36)}${Math.random().toString(36).slice(2, 8)}`;

  const exec = (id: string) => db.executions.get(id) ?? (() => { throw fail(404, 'not_found', 'No such execution.', 'Open the Executions list.'); })();
  const project = (id: string) => db.details.get(id) ?? (() => { throw fail(404, 'not_found', 'No such project.', 'Open the Projects list.'); })();

  function recount() {
    for (const p of db.projects) {
      const ex = [...db.executions.values()].filter((e) => e.project_id === p.id);
      p.running_executions = ex.filter((e) => e.state === 'running' || e.state === 'waiting_approval').length;
      p.failed_executions = ex.filter((e) => e.state === 'failed').length;
      p.pending_approvals = [...db.approvals.values()].filter((a) => a.project_id === p.id && a.state === 'pending').length;
      const d = db.details.get(p.id);
      if (d) Object.assign(d, p, { pending_approval_items: [...db.approvals.values()].filter((a) => a.project_id === p.id && a.state === 'pending') });
    }
  }

  function attention(): AttentionItem[] {
    const items: AttentionItem[] = [];
    if (db.chainBroken) items.push({ kind: 'audit_chain', label: 'Audit chain broken', subject: 'Verification failed at record 1,212', since: iso(now() - 600_000), ref_type: 'audit', ref_id: 'chain' });
    if (db.resetAlert) items.push({ kind: 'passphrase_reset', label: 'Passphrase was reset', subject: 'Reset from a terminal without the old passphrase. If it was not you, treat approvals since then as untrusted.', since: iso(now() - 300_000), ref_type: 'audit', ref_id: 'reset' });
    for (const a of db.approvals.values()) if (a.state === 'pending') items.push({ kind: 'approval', label: a.category_label, subject: a.subject, project_id: a.project_id, project_name: a.project_name, since: a.requested_at, ref_type: 'approval', ref_id: a.id });
    for (const e of db.executions.values()) {
      if (e.state === 'failed' || e.state === 'disconnected')
        items.push({ kind: e.state === 'failed' ? 'execution_failed' : 'execution_disconnected', label: e.state === 'failed' ? 'Failed' : 'Disconnected', subject: `${e.id} ${e.task_title}`, project_id: e.project_id, project_name: e.project_name, since: e.history.at(-1)?.at ?? iso(now()), ref_type: 'execution', ref_id: e.id });
    }
    return items;
  }

  function toSummary(e: ExecutionDetail) {
    const { harness_version, lease, workspace_path, workspace_locked, start_commit, usage, history, events, diff_files, recovery, queued_reason, error, result, ...s } = e;
    void [harness_version, lease, workspace_path, workspace_locked, start_commit, usage, history, events, diff_files, recovery, queued_reason, error, result];
    return s;
  }
  const toApprovalSummary = (a: ApprovalDetail) => {
    const { action, facts, impact, commands, subject_hash, allowed_decisions, step_up, phase_id, ...s } = a;
    void [action, facts, impact, commands, subject_hash, allowed_decisions, step_up, phase_id];
    return s;
  };

  function setExecState(e: ExecutionDetail, to: ExecutionDetail['state'], detail?: string) {
    const from = e.state;
    e.state = to;
    e.history.push({ at: iso(now()), state: to, detail });
    if (to !== 'running' && to !== 'waiting_approval') e.workspace_locked = false;
    recount();
    bus.emit('execution.changed', { execution_id: e.id, task_id: e.task_id, project_id: e.project_id, from, to });
    bus.emit('attention.changed', { count: attention().length });
  }

  function setTask(projectId: string, code: string, state: any, detail?: string) {
    const t = project(projectId).tasks.find((x) => x.code === code);
    if (!t) return;
    const from = t.state;
    t.state = state;
    t.state_detail = detail;
    bus.emit('task.changed', { task_id: t.id, phase_id: project(projectId).active_phase?.id ?? '', from, to: state });
  }

  function requireElevation(c: Ctx, approvalId: string | undefined, kinds: Array<'window' | 'single_use'>) {
    const el = db.elevations.get(c.headers['x-waves-elevation'] ?? '');
    if (!el || el.expires < now()) throw fail(403, 'stepup_required', 'This action needs your approval passphrase.', 'Confirm with your passphrase and try again.');
    if (!kinds.includes(el.kind) || (el.kind === 'single_use' && el.approvalId !== approvalId))
      throw fail(403, 'stepup_required', 'This confirmation does not cover this action.', 'Confirm with your passphrase again.');
    return el;
  }

  const routes: Array<[string, Handler]> = [];
  const r = (key: string, h: Handler) => routes.push([key, h]);

  r('GET /health', () => ok({ version: '0.1.0' }));
  r('GET /status', () => {
    const items = attention();
    const first = items.find((i) => i.kind === 'approval') ?? items[0];
    const ex = [...db.executions.values()];
    const status: Status = {
      server_version: '0.1.0', claude_code_version: '2.1.318',
      counts: { running: ex.filter((e) => e.state === 'running').length, queued: ex.filter((e) => e.state === 'queued').length, waiting_approval: ex.filter((e) => e.state === 'waiting_approval').length, failed: ex.filter((e) => e.state === 'failed').length, disconnected: ex.filter((e) => e.state === 'disconnected').length },
      audit: db.chainBroken
        ? { state: 'broken', records: db.seq, last_check_at: iso(now() - 60_000), broken_at_record: 1212 }
        : { state: 'verified', records: db.seq, last_check_at: iso(now() - 22 * 60_000), last_checkpoint_at: iso(now() - 92 * 60_000) },
      needs_attention: items,
      next_step: first ? { text: first.kind === 'audit_chain' ? 'Acknowledge the audit chain break on the Activity page; approvals stay blocked until then.' : first.kind === 'passphrase_reset' || first.kind === 'database_restored' ? `${first.label}: check Activity, then acknowledge it there.` : first.kind === 'approval' ? `Decide the ${first.label} approval for ${first.project_name} · ${first.subject}.` : `Look at ${first.subject} in ${first.project_name}.`, ref_type: first.ref_type, ref_id: first.ref_id } : undefined,
    };
    return ok(status);
  });
  r('GET /auth/link/{code}', (c) => (c.params.code === FIRST_VALID_LINK ? ok({ state: 'valid', expires_in_s: 41 }) : ok({ state: 'invalid' })));
  r('POST /auth/consume', (c) => {
    if (c.body?.code !== FIRST_VALID_LINK) throw fail(401, 'unauthenticated', 'This link was already used or has expired.', 'Create a new one: waves open.');
    db.signedIn = true;
    return ok(sessionInfo());
  });
  const sessionInfo = () => {
    const el = [...db.elevations.values()].filter((e) => e.kind === 'window' && e.expires > now()).at(-1);
    return { scope: 'operator' as const, csrf_token: MOCK_CSRF, expires_at: iso(now() + 12 * 3600_000), passphrase_set: db.passphraseSet, elevation: el ? { kind: el.kind, expires_at: iso(el.expires) } : undefined };
  };
  r('GET /auth/session', () => ok(sessionInfo()));
  r('POST /auth/logout', () => { db.signedIn = false; return none(); });
  r('POST /auth/stepup', (c) => {
    if (!db.passphraseSet) throw fail(409, 'passphrase_not_set', 'No approval passphrase is set.', 'Run waves setup in a terminal.');
    // A broken chain blocks decisions, not the step-up itself: the acknowledgement needs one.
    if (db.chainBroken && c.body?.approval_id) throw fail(409, 'audit_chain_broken', 'Approvals are blocked.', 'A human must acknowledge the audit chain break on the Activity page.', { record: 1212 });
    if (now() < db.lockedUntil) throw fail(429, 'rate_limited', 'Too many attempts.', 'Wait, then try again.', { retry_after_s: Math.ceil((db.lockedUntil - now()) / 1000) });
    if (c.body?.passphrase !== MOCK_PASSPHRASE) {
      db.failures += 1;
      db.seq += 1;
      if (db.failures >= 3) { db.failures = 0; db.lockedUntil = now() + 30_000; throw fail(429, 'rate_limited', 'Too many attempts.', 'Wait, then try again.', { retry_after_s: 30 }); }
      throw fail(403, 'stepup_failed', 'Wrong passphrase.', 'Nothing was approved.', { attempts_left: 3 - db.failures });
    }
    db.failures = 0;
    const approvalId: string | undefined = c.body.approval_id;
    const kind = approvalId ? 'single_use' : 'window';
    const expires = now() + (kind === 'window' ? db.settings.elevation_window_min * 60_000 : 120_000);
    const sec = secret('elv');
    db.elevations.set(sec, { kind, expires, approvalId });
    return ok({ elevation_secret: sec, kind, expires_at: iso(expires), approval_id: approvalId });
  });
  r('POST /auth/stepup/revoke', (c) => { db.elevations.delete(c.headers['x-waves-elevation'] ?? ''); return none(); });
  r('GET /audit', () => ok({ items: db.audit, status: (attentionStatus()) }));
  const attentionStatus = () => (db.chainBroken ? { state: 'broken' as const, records: db.seq, last_check_at: iso(now()), broken_at_record: 1212 } : { state: 'verified' as const, records: db.seq, last_check_at: iso(now() - 22 * 60_000), last_checkpoint_at: iso(now() - 92 * 60_000) });
  r('POST /audit/acknowledge', (c) => {
    requireElevation(c, undefined, ['window', 'single_use']);
    db.chainBroken = false;
    db.resetAlert = false;
    bus.emit('attention.changed', { count: attention().length });
    return ok(attentionStatus());
  });
  r('POST /backup', () => { db.settings.last_backup_at = iso(now()); db.settings.last_backup_bytes = 2_130_000; return ok({ at: iso(now()), bytes: 2_130_000 }); });

  r('GET /projects', () => { recount(); return ok({ items: db.projects }); });
  r('POST /projects', (c) => {
    const path = String(c.body?.path ?? '').trim();
    if (!path.startsWith('/') && !path.startsWith('~')) throw fail(422, 'validation_failed', 'The path must be absolute.', 'Use a path such as ~/work/my-project.');
    if (db.projects.some((p) => p.path === path)) throw fail(409, 'conflict', 'This folder is already registered.', 'Open it from the Projects list.');
    const name = String(c.body?.name || path.split('/').filter(Boolean).at(-1));
    const p = { id: secret('prj'), name, path, is_git: true, branch: 'main', base_branch: 'main', active: false, progress_pct: 0, running_executions: 0, failed_executions: 0, pending_approvals: 0, guard_policy: 'off' as const };
    db.projects.push(p);
    db.details.set(p.id, { ...p, phases: [], tasks: [], pending_approval_items: [], requirements: [], planning: {} });
    db.requirements.set(p.id, []);
    db.workflows.set(p.id, { versions: [] });
    db.guard.set(p.id, { policy: 'off', installed: false, would_deny_24h: 0, not_defended: db.guard.get('prj_billing')!.not_defended, unmanaged_sessions: 'allow' });
    db.guardDecisions.set(p.id, []);
    bus.emit('project.changed', { project_id: p.id });
    return ok(p, 201);
  });
  r('GET /projects/{id}', (c) => { recount(); return ok(project(c.params.id!)); });
  r('POST /projects/{id}/activate', (c) => {
    project(c.params.id!);
    for (const p of db.projects) p.active = p.id === c.params.id;
    recount();
    bus.emit('project.changed', { project_id: c.params.id! });
    return ok(db.projects.find((p) => p.id === c.params.id));
  });
  r('GET /settings', () => ok(db.settings));
  r('PATCH /settings', (c) => {
    const b = c.body ?? {};
    // Unknown keys are refused, so a body can never reach hard caps, the listen address or LAN access.
    const known = ['elevation_window_min', 'max_concurrent', 'heartbeat_interval_s', 'log_retention_days'];
    for (const k of Object.keys(b)) if (!known.includes(k)) throw fail(422, 'validation_failed', `The setting "${k}" cannot be changed here.`, 'Use the documented settings only.');
    const sec = b.elevation_window_min !== undefined || b.max_concurrent !== undefined;
    if (sec) requireElevation(c, undefined, ['window', 'single_use']);
    const bad = (m: string) => fail(422, 'validation_failed', m, 'Use a value in the allowed range.');
    if (b.max_concurrent !== undefined && !(b.max_concurrent >= 1 && b.max_concurrent <= db.settings.hard_cap)) throw bad(`Concurrent executions must be 1 to ${db.settings.hard_cap}.`);
    if (b.elevation_window_min !== undefined && !(b.elevation_window_min >= 0 && b.elevation_window_min <= 30)) throw bad('The elevation window is 0 to 30 minutes.');
    if (b.heartbeat_interval_s !== undefined && !(b.heartbeat_interval_s >= 15 && b.heartbeat_interval_s <= 300)) throw bad('The heartbeat interval is 15 to 300 seconds.');
    if (b.log_retention_days !== undefined && !(b.log_retention_days >= 1 && b.log_retention_days <= 365)) throw bad('Log retention is 1 to 365 days.');
    Object.assign(db.settings, Object.fromEntries(Object.entries(b).filter(([, v]) => v !== undefined)));
    return ok(db.settings);
  });

  r('GET /projects/{id}/requirements', (c) => (project(c.params.id!), ok({ items: db.requirements.get(c.params.id!) ?? [] })));
  r('POST /projects/{id}/requirements', (c) => {
    project(c.params.id!);
    const b = c.body ?? {};
    if (b.kind === 'file' && !String(b.path ?? '').trim()) throw fail(422, 'validation_failed', 'A file requirement needs a path.', 'Enter the path of the file.');
    if (b.kind === 'text' && !String(b.text ?? '').trim()) throw fail(422, 'validation_failed', 'A text requirement cannot be empty.', 'Write the requirement.');
    const item = { id: secret('req'), source: b.kind === 'file' ? b.path : 'Inline: ' + String(b.text).slice(0, 60), kind: b.kind, recorded_hash: b.kind === 'file' ? '00ab…c0de' : undefined, hash_now: b.kind === 'file' ? '00ab…c0de' : undefined, added_at: iso(now()), state: 'unchanged' as const };
    db.requirements.get(c.params.id!)!.unshift(item);
    project(c.params.id!).requirements.unshift({ source: item.source, state: 'unchanged' });
    bus.emit('project.changed', { project_id: c.params.id! });
    return ok(item, 201);
  });
  r('POST /projects/{id}/planning-requests', (c) => {
    const d = project(c.params.id!);
    const req = { id: secret('plr'), state: 'waiting' as const, created_at: iso(now()) };
    d.planning = { ...d.planning, request: req };
    return ok(req, 201);
  });
  r('GET /projects/{id}/workflow', (c) => (project(c.params.id!), ok(db.workflows.get(c.params.id!))));
  r('GET /workflow-versions/{id}', (c) => ok(db.versions.get(c.params.id!) ?? (() => { throw fail(404, 'not_found', 'No such workflow version.', 'Open the project workflow.'); })()));
  r('POST /workflow-versions/{id}/versions', (c) => {
    const v = db.versions.get(c.params.id!);
    if (!v) throw fail(404, 'not_found', 'No such workflow version.');
    if (v.state === 'draft') throw fail(409, 'invalid_state', 'This version is already a draft.', 'Edit it or submit it.');
    const next = { ...structuredClone(v), id: secret('wfv'), number: v.number + 1, state: 'draft' as const, created_at: iso(now()) };
    db.versions.set(next.id, next);
    db.workflows.get(v.project_id)!.versions.unshift({ id: next.id, number: next.number, state: 'draft', content_hash: next.content_hash, created_at: next.created_at });
    bus.emit('workflow.changed', { project_id: v.project_id, version_id: next.id, state: 'draft' });
    return ok({ id: next.id, number: next.number, state: 'draft', content_hash: next.content_hash, created_at: next.created_at }, 201);
  });
  r('POST /workflow-versions/{id}/submit', (c) => {
    const v = db.versions.get(c.params.id!);
    if (!v) throw fail(404, 'not_found', 'No such workflow version.');
    if (v.state !== 'draft') throw fail(409, 'invalid_state', 'Only a draft can be submitted.', 'Create a new version first.');
    v.state = 'awaiting_approval';
    db.workflows.get(v.project_id)!.versions.find((x) => x.id === v.id)!.state = 'awaiting_approval';
    const apr: ApprovalDetail = {
      id: secret('apr'), category: 'workflow', category_label: 'Workflow', state: 'pending', project_id: v.project_id, project_name: v.project_name, subject: `Workflow v${v.number}`, requested_by: 'Waves', requested_at: iso(now()),
      reject_needs_stepup: true, action: `Approve workflow version ${v.number}`, facts: [{ label: 'Content hash', value: v.content_hash, mono: true }, { label: 'Phases', value: String(v.phases.length) }],
      impact: 'Approving fixes exactly the commands listed. Waves will run them on this machine when a phase reaches validation.',
      commands: v.phases.flatMap((p) => p.validators.filter((x) => x.command).map((x) => x.command!)), subject_hash: 'sh_' + v.id, allowed_decisions: ['approve', 'reject'], step_up: 'window_ok',
    };
    db.approvals.set(apr.id, apr);
    recount();
    bus.emit('workflow.changed', { project_id: v.project_id, version_id: v.id, state: 'awaiting_approval' });
    bus.emit('approval.changed', { approval_id: apr.id, project_id: v.project_id, category: 'workflow', state: 'pending' });
    bus.emit('attention.changed', { count: attention().length });
    return ok({ approval_id: apr.id });
  });
  r('GET /phases/{id}/review', (c) => ok(db.reviews.get(c.params.id!) ?? (() => { throw fail(404, 'not_found', 'This phase has no review package.', 'A package exists once a phase reaches review.'); })()));
  r('POST /phases/{id}/reopen', (c) => {
    const rev = db.reviews.get(c.params.id!);
    if (!rev) throw fail(404, 'not_found', 'No such phase.');
    requireElevation(c, undefined, ['window']);
    if (rev.phase.state !== 'revision_required') throw fail(409, 'invalid_state', 'Only a phase whose review was rejected can reopen tasks.');
    const ids: string[] = c.body?.task_ids ?? [];
    if (!ids.length) throw fail(422, 'validation_failed', 'Choose at least one task to reopen.');
    if (!String(c.body?.reason ?? '').trim()) throw fail(422, 'validation_failed', 'Reopening needs a reason.', 'Say what must be redone.');
    for (const t of rev.tasks) if (ids.includes(t.id)) t.state = 'ready';
    const from = rev.phase.state;
    rev.phase.state = 'in_progress';
    bus.emit('phase.changed', { phase_id: rev.phase.id, project_id: rev.project_id, from, to: 'in_progress' });
    return none();
  });
  r('POST /phases/{id}/validation-runs/{vid}/confirm', (c) => {
    const rev = db.reviews.get(c.params.id!);
    const v = rev?.validators.find((x) => x.id === c.params.vid);
    const item = v?.items?.[Number(c.body?.index)];
    if (!rev || !v || !item) throw fail(404, 'not_found', 'No such checklist item.');
    requireElevation(c, undefined, ['window']);
    item.confirmed = true;
    if (v.items!.every((i) => i.confirmed)) v.state = 'passed';
    return ok(v);
  });

  r('GET /approvals', (c) => {
    const st = c.query.get('state');
    const items = [...db.approvals.values()].filter((a) => !st || a.state === st).sort((a, b) => b.requested_at.localeCompare(a.requested_at));
    return ok({ items: items.map(toApprovalSummary) });
  });
  r('GET /approvals/{id}', (c) => ok(db.approvals.get(c.params.id!) ?? (() => { throw fail(404, 'not_found', 'No such approval.', 'Open the approval inbox.'); })()));
  r('POST /approvals/{id}/decision', (c) => {
    const a = db.approvals.get(c.params.id!);
    if (!a) throw fail(404, 'not_found', 'No such approval.', 'Open the approval inbox.');
    const b = c.body ?? {};
    if (a.state !== 'pending') throw fail(409, 'invalid_state', `This approval is already ${a.state}.`, 'Nothing more to do.');
    if (db.chainBroken) throw fail(409, 'audit_chain_broken', 'Approvals are blocked.', 'A human must acknowledge the audit chain break first.', { record: 1212 });
    const rev = a.phase_id ? db.reviews.get(a.phase_id) : undefined;
    if (b.decision === 'approve') {
      const el = requireElevation(c, a.id, a.step_up === 'every_time' ? ['single_use'] : ['single_use', 'window']);
      if (b.subject_hash !== a.subject_hash) throw fail(409, 'stale_subject', 'What you are approving changed after you opened it.', 'Reload the approval and review it again.');
      if (rev?.requires_override_reason && !String(b.override_reason ?? '').trim()) throw fail(422, 'validation_failed', 'A failed validator needs a written reason to approve.', 'Write the override reason.');
      if (el.kind === 'single_use') c.db.elevations.delete(c.headers['x-waves-elevation']!);
    } else if (b.decision === 'reject') {
      if (!String(b.feedback ?? '').trim()) throw fail(422, 'validation_failed', 'Rejecting needs feedback.', 'Say what must change.');
      if (a.reject_needs_stepup) requireElevation(c, a.id, ['single_use', 'window']);
    } else throw fail(422, 'validation_failed', 'The decision must be approve or reject.');
    const approve = b.decision === 'approve';
    a.state = approve ? 'approved' : 'rejected';
    a.decided_at = iso(now());
    a.allowed_decisions = [];
    db.seq += 2;
    db.audit.unshift({ seq: db.seq, at: iso(now()), event: approve ? 'approval.approved' : 'approval.rejected', actor_scope: a.reject_needs_stepup || approve ? 'human' : 'operator', subject: `${a.project_name} ${a.subject}`, detail: approve ? 'passphrase · web · single use' : 'feedback recorded' });
    if (a.category === 'phase_completion' && rev) {
      const to = approve ? 'completed' : 'revision_required';
      const from = rev.phase.state;
      rev.phase.state = to;
      const d = project(a.project_id);
      d.phases.forEach((p) => { if (p.id === rev.phase.id) p.state = to; });
      if (d.active_phase?.id === rev.phase.id) d.active_phase.state = to;
      const proj = db.projects.find((p) => p.id === a.project_id)!;
      if (proj.active_phase?.id === rev.phase.id) proj.active_phase.state = to;
      bus.emit('phase.changed', { phase_id: rev.phase.id, project_id: a.project_id, from, to });
    }
    if (a.category === 'workflow') {
      const v = [...db.versions.values()].find((x) => x.project_id === a.project_id && x.state === 'awaiting_approval');
      if (v) { v.state = approve ? 'approved' : 'rejected'; db.workflows.get(v.project_id)!.versions.find((x) => x.id === v.id)!.state = v.state; bus.emit('workflow.changed', { project_id: v.project_id, version_id: v.id, state: v.state }); }
    }
    if (a.category === 'result') {
      const t = project(a.project_id).tasks.find((x) => x.state === 'in_review');
      if (t) setTask(a.project_id, t.code, approve ? 'done' : 'ready');
    }
    recount();
    bus.emit('approval.changed', { approval_id: a.id, project_id: a.project_id, category: a.category, state: a.state });
    bus.emit('attention.changed', { count: attention().length });
    return ok(a);
  });

  r('GET /executions', (c) => {
    if (scenario === 'error') throw fail(503, 'unavailable', 'The service could not read the execution list.', 'Retry, or run waves doctor.', { });
    if (scenario === 'empty') return ok({ items: [] });
    let list = [...db.executions.values()].sort((a, b) => (b.started_at ?? iso(now())).localeCompare(a.started_at ?? iso(now())));
    const st = c.query.get('state'), pid = c.query.get('project_id');
    if (st) list = list.filter((e) => e.state === st);
    if (pid) list = list.filter((e) => e.project_id === pid);
    return ok({ items: list.map(toSummary) });
  });
  r('GET /executions/{id}', (c) => ok(exec(c.params.id!)));
  r('GET /executions/{id}/log', (c) => {
    const bytes = new TextEncoder().encode(db.logs.get(exec(c.params.id!).id) ?? '');
    return { status: 200, text: new TextDecoder().decode(bytes.slice(Number(c.query.get('offset') ?? 0))) };
  });
  r('GET /executions/{id}/diff', (c) => ({ status: 200, text: db.diffs.get(exec(c.params.id!).id) ?? '' }));
  r('GET /executions/{id}/context', (c) => {
    const ctx = db.contexts.get(exec(c.params.id!).id);
    if (!ctx) throw fail(404, 'not_found', 'No context was delivered for this execution.', 'Context is built when the agent claims the task.');
    return ok(ctx);
  });
  r('POST /executions/{id}/cancel', (c) => {
    const e = exec(c.params.id!);
    if (!['running', 'queued', 'waiting_approval', 'paused', 'starting'].includes(e.state)) throw fail(409, 'invalid_state', `An execution that is ${e.state} cannot be interrupted.`);
    setExecState(e, 'cancelled', 'interrupted by operator');
    return ok(e, 202);
  });
  r('POST /executions/{id}/resolve', (c) => {
    const e = exec(c.params.id!);
    if (!['interrupted', 'disconnected'].includes(e.state)) throw fail(409, 'invalid_state', 'Only an interrupted or disconnected execution needs a decision.');
    const map = { fail: 'failed', complete_partial: 'completed', resume: 'running', retry_fresh: 'cancelled' } as const;
    const d = c.body?.decision as keyof typeof map;
    if (!map[d]) throw fail(422, 'validation_failed', 'Unknown decision.');
    setExecState(e, map[d], `resolved: ${d}`);
    e.recovery = undefined;
    setTask(e.project_id, e.task_code, d === 'complete_partial' ? 'in_review' : d === 'resume' ? 'in_progress' : d === 'retry_fresh' ? 'ready' : 'cancelled');
    return ok(e);
  });
  r('POST /executions/{id}/orphans/stop', (c) => {
    const e = exec(c.params.id!);
    if (!e.recovery?.orphan) throw fail(409, 'invalid_state', 'No orphaned process is left.');
    e.recovery.orphan = undefined;
    return ok(e);
  });

  r('GET /harnesses', () => ok({ items: db.harnesses }));
  r('GET /projects/{id}/guard', (c) => {
    const g = db.guard.get(c.params.id!) ?? (() => { throw fail(404, 'not_found', 'No such project.'); })();
    return ok(scenario === 'drift' && g.installed ? { ...g, installed: false, drift: 'the Waves hook entry is missing from .claude/settings.local.json' } : g);
  });
  r('GET /projects/{id}/guard/decisions', (c) => ok({ items: db.guardDecisions.get(c.params.id!) ?? [] }));
  const planOf = (d: { id: string; path: string }) => {
    const lines = [
      '+  "mcpServers": {', '+    "waves": { "command": "waves", "args": ["mcp"] }', '+  },',
      '+  "hooks": { "PreToolUse": [{ "matcher": "Write|Edit|MultiEdit|NotebookEdit|Bash|mcp__waves__.*",',
      `+    "hooks": [{ "type": "command", "command": "/bin/sh -c 'waves hook || exit 2'", "timeout": 5 }] }] }`,
    ];
    return { plan_hash: 'ph_' + d.id + '_' + lines.join('').length.toString(16), file: `${d.path}/.claude/settings.local.json`, lines };
  };
  r('GET /projects/{id}/agent/connect-plan', (c) => ok({ ...planOf(project(c.params.id!)), applied: false }));
  r('POST /projects/{id}/agent/connect', (c) => {
    const d = project(c.params.id!);
    const plan = planOf(d);
    if (c.body?.plan_hash !== plan.plan_hash) throw fail(409, 'stale_subject', 'The edits changed after you reviewed them.', 'Reload the plan and review it again.');
    requireElevation(c, undefined, ['window', 'single_use']);
    const g = db.guard.get(d.id)!;
    g.installed = true;
    if (g.policy === 'off') g.policy = 'audit';
    const p = db.projects.find((x) => x.id === d.id)!;
    p.guard_policy = g.policy;
    d.guard_policy = g.policy;
    d.harness = { name: 'Claude Code', version: '2.1.318', connected: true, guard_policy: g.policy, would_deny_24h: 0, credential_active: true };
    bus.emit('harness.changed', { harness_id: 'hrn_claude', project_id: d.id });
    return ok({ ...plan, applied: true });
  });
  r('PATCH /projects/{id}/settings', (c) => {
    const d = project(c.params.id!);
    requireElevation(c, undefined, ['window', 'single_use']);
    const g = db.guard.get(d.id)!;
    const b = c.body ?? {};
    if (b.guard_policy && !['off', 'audit', 'enforce'].includes(b.guard_policy)) throw fail(422, 'validation_failed', 'Unknown guard policy.');
    if (b.guard_policy && b.guard_policy !== 'off' && !g.installed) throw fail(409, 'invalid_state', 'The guard hook is not installed in this project.', 'Connect Claude Code first.');
    if (b.guard_policy) g.policy = b.guard_policy;
    if (b.unmanaged_sessions) g.unmanaged_sessions = b.unmanaged_sessions;
    const p = db.projects.find((x) => x.id === d.id)!;
    p.guard_policy = g.policy;
    d.guard_policy = g.policy;
    if (d.harness) d.harness.guard_policy = g.policy;
    db.seq += 1;
    db.audit.unshift({ seq: db.seq, at: iso(now()), event: 'guard.policy_changed', actor_scope: 'human', subject: d.name, detail: `policy ${g.policy}` });
    bus.emit('harness.changed', { harness_id: 'hrn_claude', project_id: d.id });
    return ok(g);
  });
  r('POST /projects/{id}/agent/disconnect', (c) => {
    const d = project(c.params.id!);
    requireElevation(c, undefined, ['window', 'single_use']);
    const g = db.guard.get(d.id)!;
    g.installed = false;
    g.policy = 'off';
    const p = db.projects.find((x) => x.id === d.id)!;
    p.guard_policy = 'off';
    d.guard_policy = 'off';
    d.harness = undefined;
    bus.emit('harness.changed', { harness_id: 'hrn_claude', project_id: d.id });
    return ok({ plan_hash: 'ph_removed', file: `${d.path}/.claude/settings.local.json`, lines: ['-  "mcpServers": { "waves": … }', '-  "hooks": { "PreToolUse": [ … ] }'], applied: true });
  });
  r('GET /events', () => { throw fail(406, 'unsupported', 'Open this route with EventSource.'); });

  // ---- dispatch ----
  const compiled = routes.map(([key, h]) => {
    const [method, tpl] = key.split(' ') as [string, string];
    const names: string[] = [];
    const re = new RegExp('^' + tpl.replace(/\{(\w+)\}/g, (_, n: string) => (names.push(n), '([^/]+)')) + '$');
    return { method, re, names, h };
  });

  const sleep = (ms: number) => new Promise((res) => setTimeout(res, ms));
  const latency = scenario === 'slow' ? 2000 : (opts.latencyMs ?? 0);

  const transport: Transport = async (req: TransportRequest): Promise<TransportResponse> => {
    if (latency) await sleep(latency);
    const [path, qs = ''] = req.url.split('?') as [string, string?];
    const method = req.method.toUpperCase();
    const route = compiled.find((x) => x.method === method && x.re.test(path));
    const respond = (o: Out): TransportResponse => ({ status: o.status ?? 200, text: o.text ?? (o.json === undefined ? '' : JSON.stringify(o.json)) });
    try {
      if (!route) throw fail(404, 'not_found', `No route ${method} ${path}.`);
      const headers = Object.fromEntries(Object.entries(req.headers).map(([k, v]) => [k.toLowerCase(), v]));
      const m = route.re.exec(path)!;
      const params = Object.fromEntries(route.names.map((n, i) => [n, decodeURIComponent(m[i + 1]!)]));
      const open = path.startsWith('/auth/link/') || path === '/auth/consume' || path === '/health';
      if (!open && !db.signedIn) throw fail(401, 'unauthenticated', 'Your session ended.', 'Open a fresh link: waves open.');
      if (method !== 'GET' && !open && headers['x-waves-csrf'] !== MOCK_CSRF) throw fail(403, 'csrf_failed', 'The request did not carry the CSRF token.', 'Reload the page.');
      return respond(route.h({ params, query: new URLSearchParams(qs), body: req.body ? JSON.parse(req.body) : undefined, headers, db, bus }));
    } catch (e) {
      if (e instanceof Fail) return respond({ status: e.status, json: { error: { code: e.code, message: e.message, next_step: e.next || undefined, request_id: 'req_' + Math.random().toString(16).slice(2, 6), ...e.extra } } });
      throw e;
    }
  };

  // Live agent: one more log line now and then, announced over the event stream.
  let tickTimer: ReturnType<typeof setInterval> | undefined;
  if (opts.tickMs) {
    const lines = ['tool     Read src/checkout/receipt.ts', 'heartbeat ok, next due in 15 s', 'tool     Edit src/checkout/persist.ts (+6 -2)', 'progress Persistence covers the cold start path'];
    let n = 0;
    tickTimer = setInterval(() => {
      const e = db.executions.get('exe_a07e52');
      if (!e || e.state !== 'running') return;
      const t = new Date().toTimeString().slice(0, 8);
      db.logs.set(e.id, (db.logs.get(e.id) ?? '') + `${t} ${lines[n++ % lines.length]}\n`);
      bus.emit('execution.log', { execution_id: e.id, offset: new TextEncoder().encode(db.logs.get(e.id)!).length });
    }, opts.tickMs);
  }

  return { transport, db, bus, routeKeys: routes.map(([k]) => k), stop: () => clearInterval(tickTimer) };
}

export type Mock = ReturnType<typeof createMock>;
