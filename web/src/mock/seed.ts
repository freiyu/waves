// Invented sample data for the mock adapter. Names, paths, ids and commands are
// fake. The story is kept consistent: states, counts and approvals agree.
import type {
  ApprovalDetail, AuditRecord, ContextPackage, ExecutionDetail, GuardDecision, Harness, PhaseReview, Project,
  ProjectDetail, Requirement, Settings, WorkflowVersion,
} from '../api/models';
import type { components } from '../api/types';

type S = components['schemas'];

/** Fake passphrase of the mock service. The real one is never typed anywhere but a terminal or the step-up prompt. */
export const MOCK_PASSPHRASE = 'mock-passphrase-ui-review'; // guard:allow fake fixture for the mock adapter
export const MOCK_CSRF = 'mock-csrf-token'; // guard:allow fake fixture for the mock adapter
export const FIRST_VALID_LINK = 'link-valid-demo';

export interface MockDb {
  now: number;
  signedIn: boolean;
  passphraseSet: boolean;
  chainBroken: boolean;
  resetAlert: boolean; // an unacknowledged offline passphrase reset
  elevations: Map<string, { kind: 'window' | 'single_use'; expires: number; approvalId?: string }>;
  failures: number;
  lockedUntil: number;
  projects: Project[];
  details: Map<string, ProjectDetail>;
  requirements: Map<string, Requirement[]>;
  workflows: Map<string, S['ContractWorkflowInfo']>;
  versions: Map<string, WorkflowVersion>;
  reviews: Map<string, PhaseReview>;
  approvals: Map<string, ApprovalDetail>;
  executions: Map<string, ExecutionDetail>;
  logs: Map<string, string>;
  diffs: Map<string, string>;
  contexts: Map<string, ContextPackage>;
  harnesses: Harness[];
  guard: Map<string, S['ContractGuardInfo']>;
  guardDecisions: Map<string, GuardDecision[]>;
  audit: AuditRecord[];
  settings: Settings;
  seq: number;
}

const NOT_DEFENDED =
  'The guard lowers the chance of edits without a lease. It is not a sandbox. It does not stop other processes, a user who removes the hook, or files written by a shell command (Bash calls are allowed and logged as unchecked). Changes made without a matching execution show up as unattributed in the phase review.';

export function seed(now = Date.now()): MockDb {
  const at = (min: number) => new Date(now - min * 60_000).toISOString();
  const ph = (id: string, code: string, title: string, state: S['ContractPhaseRef']['state']) => ({ id, code, title, state });
  const task = (id: string, code: string, title: string, state: S['ContractTaskSummary']['state'], depends: string[], extra: Partial<S['ContractTaskSummary']> = {}): S['ContractTaskSummary'] =>
    ({ id, code, title, state, mode: 'code_change', depends_on: depends, ...extra });

  const projects: Project[] = [
    {
      id: 'prj_billing', name: 'billing-api', path: '~/work/billing-api', is_git: true, branch: 'main', base_branch: 'main', active: true,
      active_phase: ph('phs_b2', 'P2', 'Persistence layer', 'in_progress'), progress_pct: 33, running_executions: 0, failed_executions: 0,
      pending_approvals: 0, guard_policy: 'audit', last_activity_at: at(1),
    },
    {
      id: 'prj_mobile', name: 'mobile-checkout', path: '~/work/mobile-checkout', is_git: true, branch: 'develop', base_branch: 'develop', active: false,
      active_phase: ph('phs_m3', 'P3', 'Payments UI', 'in_progress'), progress_pct: 58, running_executions: 1, failed_executions: 1,
      pending_approvals: 2, guard_policy: 'enforce', last_activity_at: at(3),
    },
    {
      id: 'prj_docs', name: 'docs-site', path: '~/work/docs-site', is_git: true, branch: 'main', base_branch: 'main', active: false,
      active_phase: ph('phs_d1', 'P1', 'Content audit', 'awaiting_phase_review'), progress_pct: 75, running_executions: 0, failed_executions: 0,
      pending_approvals: 1, guard_policy: 'off', last_activity_at: at(12),
    },
    {
      id: 'prj_scratch', name: 'scratch-notes', path: '~/work/scratch-notes', is_git: false, active: false, progress_pct: 0,
      running_executions: 0, failed_executions: 0, pending_approvals: 0, guard_policy: 'off', last_activity_at: at(60 * 24 * 6),
    },
  ];

  // ---- approvals ----
  const approvals = new Map<string, ApprovalDetail>();
  const addApproval = (a: ApprovalDetail) => approvals.set(a.id, a);
  addApproval({
    id: 'apr_d1done', category: 'phase_completion', category_label: 'Phase Completion', state: 'pending', project_id: 'prj_docs', project_name: 'docs-site',
    subject: 'P1 Content audit', requested_by: 'Waves', requested_at: at(12), action: 'Mark phase P1 as completed',
    facts: [
      { label: 'Phase', value: 'P1 Content audit' }, { label: 'Workflow', value: 'docs-site v1 (approved)' },
      { label: 'Validators', value: '1 of 3 failed: build site (exit 2)' }, { label: 'Package hash', value: 'a41c…7e09', mono: true },
    ],
    impact: 'P1 becomes Completed. P2 can then be submitted for its own approval. Nothing is merged.',
    commands: ['make linkcheck', 'make build'], subject_hash: 'sh_d1_a41c7e09', allowed_decisions: ['approve', 'reject'], step_up: 'every_time', reject_needs_stepup: true, phase_id: 'phs_d1',
  });
  addApproval({
    id: 'apr_m3t2', category: 'result', category_label: 'Result', state: 'pending', project_id: 'prj_mobile', project_name: 'mobile-checkout',
    subject: 'm3-t2 Wire Apple Pay sheet', requested_by: 'Claude Code', requested_at: at(41), action: 'Accept the result of task m3-t2',
    facts: [
      { label: 'Phase', value: 'P3 Payments UI' }, { label: 'Task', value: 'm3-t2 Wire Apple Pay sheet' }, { label: 'Execution', value: 'exe_b220f1', mono: true },
      { label: 'Changes', value: '7 files · +244 -31' }, { label: 'Agent summary', value: 'Sheet opens from the picker and returns the token. Not verified by Waves.' },
    ],
    impact: 'The task becomes Done and dependent tasks can be claimed.', commands: [], subject_hash: 'sh_m3t2_b220f1', allowed_decisions: ['approve', 'reject'], step_up: 'window_ok', reject_needs_stepup: false,
  });
  addApproval({
    id: 'apr_mwf2', category: 'workflow', category_label: 'Workflow', state: 'pending', project_id: 'prj_mobile', project_name: 'mobile-checkout',
    subject: 'Workflow v2 (revised scope)', requested_by: 'Waves', requested_at: at(27), action: 'Approve workflow version 2',
    facts: [
      { label: 'Version', value: 'v2, replaces v1' }, { label: 'Content hash', value: '7d20…c4be', mono: true },
      { label: 'Phases', value: '4, one added (P4 Release prep)' }, { label: 'Validators', value: '5 commands, 1 checklist' },
    ],
    impact: 'Approving fixes exactly the commands listed. Waves will run them on this machine when a phase reaches validation.',
    commands: ['npm test -- --run', 'npm run typecheck', 'npm run build'], subject_hash: 'sh_mwf2_7d20c4be', allowed_decisions: ['approve', 'reject'], step_up: 'window_ok', reject_needs_stepup: true,
  });
  const decided = (id: string, category: ApprovalDetail['category'], label: string, project: string, pid: string, subject: string, min: number): ApprovalDetail => ({
    id, category, category_label: label, state: 'approved', project_id: pid, project_name: project, subject, requested_by: 'Waves', requested_at: at(min + 3),
    decided_at: at(min), action: 'Decided', facts: [], impact: '', commands: [], subject_hash: 'sh_' + id, allowed_decisions: [], step_up: 'every_time', reject_needs_stepup: true,
  });
  addApproval(decided('apr_bp2start', 'phase_start', 'Phase Start', 'billing-api', 'prj_billing', 'P2 Persistence layer', 230));
  addApproval(decided('apr_bp1done', 'phase_completion', 'Phase Completion', 'billing-api', 'prj_billing', 'P1 Schema design', 250));

  // ---- executions ----
  const executions = new Map<string, ExecutionDetail>();
  const ex = (e: Omit<ExecutionDetail, 'harness' | 'mode' | 'events' | 'history' | 'diff_files' | 'workspace_locked'> & Partial<ExecutionDetail>): ExecutionDetail => ({
    harness: 'Claude Code', harness_version: '2.1.318', mode: 'harness_driven', events: [], history: [], diff_files: 0, workspace_locked: false, ...e,
  });
  executions.set('exe_a07e52', ex({
    id: 'exe_a07e52', task_id: 'tsk_m3t3', task_code: 'm3-t3', task_title: 'Persist selected method in checkout state', project_id: 'prj_mobile', project_name: 'mobile-checkout',
    state: 'running', started_at: at(7), duration_s: 423, workspace_path: '~/work/mobile-checkout', workspace_locked: true, start_commit: '4be19a7', diff_files: 3,
    lease: { id: 'lse_5c1e', expires_at: new Date(now + 74_000).toISOString(), heartbeat_interval_s: 15 },
    usage: { tokens_in: 41220, tokens_out: 6904, cost_usd: 0.31 },
    history: [{ at: at(7.05), state: 'queued' }, { at: at(7.03), state: 'starting' }, { at: at(7), state: 'running' }],
    events: [
      { at: at(7), kind: 'claim', message: 'Lease lse_5c1e granted (ttl 90 s)' }, { at: at(5), kind: 'progress', message: 'Reading checkout state module' },
      { at: at(1), kind: 'progress', message: 'Selected method now persisted between app launches' },
    ],
  }));
  executions.set('exe_8f3a1c', ex({
    id: 'exe_8f3a1c', task_id: 'tsk_b2t3', task_code: 'p2-t3', task_title: 'Add idempotency keys to POST /charges', project_id: 'prj_billing', project_name: 'billing-api',
    state: 'disconnected', started_at: at(21), duration_s: 1122, workspace_path: '~/work/billing-api', workspace_locked: true, start_commit: '2e7c0d1', diff_files: 6,
    history: [{ at: at(21), state: 'queued' }, { at: at(21), state: 'running' }, { at: at(3), state: 'disconnected', detail: '3 missed heartbeats' }, { at: at(0), state: 'blocked', detail: 'recovery required' }],
    events: [{ at: at(4), kind: 'heartbeat', message: 'Last heartbeat' }, { at: at(3), kind: 'state', message: 'Marked Disconnected after 3 missed heartbeats' }],
    recovery: {
      partial_files: 6, partial_added: 188, partial_removed: 21, log_lines: 184, last_log_at: at(4), orphan: { count: 1, command: 'go test ./...' },
      resume_session: '4b0f…c71a', reason: 'Heartbeats stopped and the service saw the session end. Waves did not rerun the task.',
      timeline: [
        { at: at(21), state: 'running', detail: 'Claimed, lease granted' }, { at: at(4), state: 'running', detail: 'Last heartbeat' },
        { at: at(3), state: 'disconnected', detail: 'Marked Disconnected' }, { at: at(0), state: 'blocked', detail: 'Task blocked: recovery required' },
      ],
    },
  }));
  executions.set('exe_c41d09', ex({
    id: 'exe_c41d09', task_id: 'tsk_m3t4', task_code: 'm3-t4', task_title: 'Fix flaky checkout test', project_id: 'prj_mobile', project_name: 'mobile-checkout',
    state: 'failed', started_at: at(6), duration_s: 171, workspace_path: '~/work/mobile-checkout', start_commit: '9a3d5c2', diff_files: 1,
    history: [{ at: at(6), state: 'queued' }, { at: at(6), state: 'running' }, { at: at(3), state: 'failed', detail: 'validator exit 1' }],
    error: 'Validator "npm test -- --run" exited with status 1 (2 failing tests in src/checkout/cart.test.ts).',
    events: [{ at: at(3), kind: 'result', message: 'Agent reported failure: tests still flaky after retry removal' }],
  }));
  executions.set('exe_d5e8a0', ex({
    id: 'exe_d5e8a0', task_id: 'tsk_b2t5', task_code: 'p2-t5', task_title: 'Audit query plans', project_id: 'prj_billing', project_name: 'billing-api',
    state: 'queued', state_detail: 'waiting for a free slot', queued_reason: 'The global limit of 2 executions is reached (exe_a07e52 running, exe_8f3a1c disconnected).',
    workspace_path: '~/work/billing-api', history: [{ at: at(2), state: 'queued' }],
  }));
  executions.set('exe_b220f1', ex({
    id: 'exe_b220f1', task_id: 'tsk_m3t2', task_code: 'm3-t2', task_title: 'Wire Apple Pay sheet', project_id: 'prj_mobile', project_name: 'mobile-checkout',
    state: 'completed', started_at: at(75), duration_s: 2050, workspace_path: '~/work/mobile-checkout', start_commit: '71c8e04', diff_files: 7,
    usage: { tokens_in: 88100, tokens_out: 14300, cost_usd: 0.74 }, result: 'Sheet opens from the picker and returns the token. Not verified by Waves.',
    history: [{ at: at(75), state: 'queued' }, { at: at(75), state: 'running' }, { at: at(41), state: 'completed' }],
  }));
  executions.set('exe_3d7c19', ex({
    id: 'exe_3d7c19', task_id: 'tsk_b2t2', task_code: 'p2-t2', task_title: 'Migrate invoice totals to decimal', project_id: 'prj_billing', project_name: 'billing-api',
    state: 'completed', started_at: at(140), duration_s: 1611, workspace_path: '~/work/billing-api', start_commit: '0f66b3a', diff_files: 9,
    history: [{ at: at(140), state: 'queued' }, { at: at(140), state: 'running' }, { at: at(113), state: 'completed' }],
  }));
  executions.set('exe_91b2d4', ex({
    id: 'exe_91b2d4', task_id: 'tsk_b2t1', task_code: 'p2-t1', task_title: 'Add decimal money type', project_id: 'prj_billing', project_name: 'billing-api',
    state: 'completed', started_at: at(200), duration_s: 740, workspace_path: '~/work/billing-api', start_commit: 'c0a91e7', diff_files: 4,
    history: [{ at: at(200), state: 'queued' }, { at: at(200), state: 'running' }, { at: at(188), state: 'completed' }],
  }));

  const logs = new Map<string, string>();
  logs.set('exe_a07e52', [
    '11:42:03 claim    m3-t3 granted lease lse_5c1e (ttl 90 s)',
    '11:42:05 tool     Read src/checkout/state.ts',
    '11:43:19 tool     Edit src/checkout/state.ts (+31 -9)',
    '11:44:40 tool     Bash npm test -- --run src/checkout',
    '11:44:52 ok       src/checkout/state.test.ts  14 passed',
    '11:46:10 tool     Edit src/checkout/persist.ts (+58 -0)',
    '11:48:01 heartbeat ok, next due in 15 s',
    '11:48:16 progress Selected method now persisted between app launches',
    '',
  ].join('\n'));
  logs.set('exe_8f3a1c', Array.from({ length: 12 }, (_, i) => `11:${30 + i} tool     Edit internal/billing/charges.go`).join('\n') + '\n');
  logs.set('exe_c41d09', '11:17:02 claim    m3-t4 granted lease\n11:18:40 tool     Edit src/checkout/cart.test.ts (+4 -12)\n11:19:55 tool     Bash npm test -- --run\n11:19:58 failed   2 failing tests in src/checkout/cart.test.ts\n');
  const diffs = new Map<string, string>();
  diffs.set('exe_a07e52', [
    'diff --git a/src/checkout/state.ts b/src/checkout/state.ts', '--- a/src/checkout/state.ts', '+++ b/src/checkout/state.ts', '@@ -12,9 +12,12 @@',
    ' export function selectMethod(state: CheckoutState, id: string): CheckoutState {',
    '-  return { ...state, method: id };', '+  const next = { ...state, method: id };', '+  persistMethod(next.method);', '+  return next;', ' }',
    'diff --git a/src/checkout/persist.ts b/src/checkout/persist.ts', 'new file mode 100644', '--- /dev/null', '+++ b/src/checkout/persist.ts', '@@ -0,0 +1,4 @@',
    '+export function persistMethod(id: string): void {', "+  storage.set('checkout.method', id);", '+}', '',
  ].join('\n'));
  diffs.set('exe_8f3a1c', ['diff --git a/internal/billing/charges.go b/internal/billing/charges.go', '--- a/internal/billing/charges.go', '+++ b/internal/billing/charges.go', '@@ -40,6 +40,9 @@', '-func (c *Charges) Create(ctx context.Context, in Input) error {', '+func (c *Charges) Create(ctx context.Context, key string, in Input) error {', ''].join('\n'));
  const contexts = new Map<string, ContextPackage>();
  contexts.set('exe_a07e52', {
    generated_at: at(7), size_bytes: 3912,
    sections: [
      { title: 'Task', content: 'm3-t3 Persist selected method in checkout state\nMode: code_change' },
      { title: 'Acceptance criteria', content: '1. The selected payment method survives an app restart.\n2. Selecting a method never blocks the UI thread.\n3. Existing checkout tests pass.' },
      { title: 'Rules', content: 'Work only inside the leased workspace. Report progress and blockers through Waves. Do not change files outside the task scope.' },
    ],
  });

  // ---- workflow ----
  const versions = new Map<string, WorkflowVersion>();
  const v = (id: string, num: number, state: S['ContractWorkflowVersionSummary']['state'], hash: string, pid: string, pname: string, phases: S['ContractWorkflowPhase'][], proposal?: string): WorkflowVersion =>
    ({ id, number: num, state, content_hash: hash, created_at: at(num * 30), project_id: pid, project_name: pname, proposal_id: proposal, phases });
  versions.set('wfv_b2', v('wfv_b2', 2, 'approved', 'e1d4…90aa', 'prj_billing', 'billing-api', [{
    id: 'phs_b2', code: 'P2', title: 'Persistence layer', objective: 'Persist money and invoices with decimal precision.',
    tasks: [{ code: 'p2-t3', title: 'Add idempotency keys to POST /charges', mode: 'code_change', acceptance: ['Repeated key returns the first response'] }],
    validators: [{ id: 'val_b2a', kind: 'command', name: 'unit tests', command: 'go test ./... -race', timeout_s: 300 }],
  }]));
  versions.set('wfv_b3', v('wfv_b3', 3, 'draft', 'b3f9…a1c2', 'prj_billing', 'billing-api', [{
    id: 'phs_b2', code: 'P2', title: 'Persistence layer', objective: 'Persist money and invoices with decimal precision.',
    tasks: [
      { code: 'p2-t2', title: 'Migrate invoice totals to decimal', mode: 'code_change', acceptance: ['Totals stored as decimal(18,4)', 'Legacy export matches on 3 sample invoices', 'Migration is reversible'] },
      { code: 'p2-t3', title: 'Add idempotency keys to POST /charges', mode: 'code_change', acceptance: ['Repeated key returns the first response', 'Keys expire after 24 h', 'Concurrent requests do not double-charge', 'Covered by tests'] },
    ],
    validators: [
      { id: 'val_b2a', kind: 'command', name: 'unit tests', command: 'go test ./... -race', timeout_s: 300 },
      { id: 'val_b2b', kind: 'command', name: 'migrations apply cleanly', command: 'make migrate-check', timeout_s: 120 },
      { id: 'val_b2c', kind: 'checklist', name: 'manual review', items: ['Totals match legacy export on 3 sample invoices'] },
    ],
  }], 'prp_41be'));
  versions.set('wfv_m2', v('wfv_m2', 2, 'awaiting_approval', '7d20…c4be', 'prj_mobile', 'mobile-checkout', [{
    id: 'phs_m4', code: 'P4', title: 'Release prep', objective: 'Prepare the checkout flow for store review.',
    tasks: [{ code: 'm4-t1', title: 'Update store screenshots', mode: 'read_only', acceptance: ['Screenshots list every checkout step'] }],
    validators: [{ id: 'val_m4a', kind: 'command', name: 'typecheck', command: 'npm run typecheck', timeout_s: 120 }],
  }]));
  versions.set('wfv_m1', v('wfv_m1', 1, 'approved', '2b81…f0d3', 'prj_mobile', 'mobile-checkout', [{
    id: 'phs_m3', code: 'P3', title: 'Payments UI', objective: 'Let the user pick and confirm a payment method.',
    tasks: [{ code: 'm3-t3', title: 'Persist selected method in checkout state', mode: 'code_change', acceptance: ['The selected method survives an app restart'] }],
    validators: [{ id: 'val_m3a', kind: 'command', name: 'unit tests', command: 'npm test -- --run', timeout_s: 300 }],
  }]));
  versions.set('wfv_d1', v('wfv_d1', 1, 'approved', '90ce…14a7', 'prj_docs', 'docs-site', [{
    id: 'phs_d1', code: 'P1', title: 'Content audit', objective: 'Find and fix broken or outdated pages.',
    tasks: [{ code: 'd1-t2', title: 'Fix broken internal links', mode: 'code_change', acceptance: ['Link check passes'] }],
    validators: [{ id: 'val_d1a', kind: 'command', name: 'link check', command: 'make linkcheck', timeout_s: 120 }, { id: 'val_d1b', kind: 'command', name: 'build site', command: 'make build', timeout_s: 300 }],
  }]));
  const workflows = new Map<string, S['ContractWorkflowInfo']>();
  workflows.set('prj_billing', { current_id: 'wfv_b2', versions: [
    { id: 'wfv_b3', number: 3, state: 'draft', content_hash: 'b3f9…a1c2', created_at: at(30) }, { id: 'wfv_b2', number: 2, state: 'approved', content_hash: 'e1d4…90aa', created_at: at(300) }] });
  workflows.set('prj_mobile', { current_id: 'wfv_m1', versions: [
    { id: 'wfv_m2', number: 2, state: 'awaiting_approval', content_hash: '7d20…c4be', created_at: at(27) }, { id: 'wfv_m1', number: 1, state: 'approved', content_hash: '2b81…f0d3', created_at: at(2000) }] });
  workflows.set('prj_docs', { current_id: 'wfv_d1', versions: [{ id: 'wfv_d1', number: 1, state: 'approved', content_hash: '90ce…14a7', created_at: at(3000) }] });
  workflows.set('prj_scratch', { versions: [] });

  // ---- phase review (docs-site P1) ----
  const reviews = new Map<string, PhaseReview>();
  reviews.set('phs_d1', {
    phase: ph('phs_d1', 'P1', 'Content audit', 'awaiting_phase_review'), project_id: 'prj_docs', project_name: 'docs-site', workflow_number: 1, generated_at: at(13),
    validators: [
      { id: 'val_d1a', name: 'link check', kind: 'command', state: 'passed', exit_code: 0, duration_s: 102 },
      { id: 'val_d1b', name: 'build site', kind: 'command', state: 'failed', exit_code: 2, duration_s: 9 },
      { id: 'val_d1c', name: 'manual review', kind: 'checklist', state: 'waiting', items: [
        { text: 'Install guide reads correctly end to end', confirmed: true }, { text: 'No page links to a removed section', confirmed: true }, { text: 'Screenshots match the current UI', confirmed: false }] },
    ],
    tasks: [
      { id: 'tsk_d1t1', code: 'd1-t1', title: 'Inventory all pages', state: 'done', duration_s: 740, attempts: 1, files_changed: 2 },
      { id: 'tsk_d1t2', code: 'd1-t2', title: 'Fix broken internal links', state: 'done', duration_s: 1611, attempts: 2, files_changed: 9 },
      { id: 'tsk_d1t3', code: 'd1-t3', title: 'Rewrite the install guide', state: 'done', duration_s: 1086, attempts: 1, files_changed: 6 },
      { id: 'tsk_d1t4', code: 'd1-t4', title: 'Audit sidebar order', state: 'cancelled', state_detail: 'Cancelled · out of scope', attempts: 0, files_changed: 0 },
    ],
    findings: [
      { kind: 'unattributed', path: 'content/install.md', detail: 'Modified 11:04, no execution holds this file' },
      { kind: 'out_of_scope', path: 'config.toml', detail: 'Changed by d1-t3 but not in its declared scope' },
    ],
    summary: 'Broken links are fixed and the install guide is rewritten. The site build fails on a missing front matter field.',
    recommendation: 'Fix the build before completing P1.', diff: { files: 17, added: 412, removed: 138, executions: 3 }, approval_id: 'apr_d1done', requires_override_reason: true,
  });

  // ---- project details ----
  const details = new Map<string, ProjectDetail>();
  const pendingOf = (pid: string) => [...approvals.values()].filter((a) => a.project_id === pid && a.state === 'pending');
  const harnessBrief = (policy: 'off' | 'audit' | 'enforce', deny: number) => ({ name: 'Claude Code', version: '2.1.318', connected: policy !== 'off', guard_policy: policy, would_deny_24h: deny, credential_active: policy !== 'off' });
  details.set('prj_billing', {
    ...projects[0]!, phases: [ph('phs_b1', 'P1', 'Schema design', 'completed'), ph('phs_b2', 'P2', 'Persistence layer', 'in_progress'), ph('phs_b3', 'P3', 'Payments API', 'planned'), ph('phs_b4', 'P4', 'Hardening', 'planned')],
    tasks_phase: 'P2 Persistence layer',
    tasks: [
      task('tsk_b2t1', 'p2-t1', 'Add decimal money type', 'done', [], { execution_id: 'exe_91b2d4' }),
      task('tsk_b2t2', 'p2-t2', 'Migrate invoice totals to decimal', 'done', ['p2-t1'], { execution_id: 'exe_3d7c19' }),
      task('tsk_b2t3', 'p2-t3', 'Add idempotency keys to POST /charges', 'blocked', ['p2-t1'], { execution_id: 'exe_8f3a1c', state_detail: 'Blocked · recovery required' }),
      task('tsk_b2t4', 'p2-t4', 'Write repository tests', 'backlog', ['p2-t3']),
      task('tsk_b2t5', 'p2-t5', 'Audit query plans', 'ready', ['p2-t1'], { mode: 'read_only', execution_id: 'exe_d5e8a0' }),
    ],
    harness: harnessBrief('audit', 2), pending_approval_items: pendingOf('prj_billing'),
    requirements: [{ source: 'docs/spec-billing.md', state: 'changed' }],
    planning: {
      request: { id: 'plr_7d21', state: 'waiting', created_at: at(2) },
      latest_proposal: { id: 'prp_3a90', version: 2, valid: false, created_at: at(9), errors: [
        { code: 'dependency_cycle', path: '/phases/1/tasks/2', message: 'p2-t2 → p2-t3 → p2-t2 forms a cycle' },
        { code: 'missing_acceptance', path: '/phases/0/tasks/0', message: 'Code-changing task needs acceptance criteria' },
        { code: 'phase_incomplete', path: '/phases/3', message: 'Phase has no validator' }] },
    },
    next_step: { text: 'Decide what happens to p2-t3 (recovery), then the read-only audit task can start.', ref_type: 'execution', ref_id: 'exe_8f3a1c' },
  });
  details.set('prj_mobile', {
    ...projects[1]!, phases: [ph('phs_m1', 'P1', 'Scaffold', 'completed'), ph('phs_m2', 'P2', 'Design system', 'completed'), ph('phs_m3', 'P3', 'Payments UI', 'in_progress'), ph('phs_m4', 'P4', 'Release prep', 'planned')],
    tasks_phase: 'P3 Payments UI',
    tasks: [
      task('tsk_m3t1', 'm3-t1', 'Add payment method picker', 'done', []),
      task('tsk_m3t2', 'm3-t2', 'Wire Apple Pay sheet', 'in_review', ['m3-t1'], { execution_id: 'exe_b220f1' }),
      task('tsk_m3t3', 'm3-t3', 'Persist selected method in checkout state', 'in_progress', ['m3-t1'], { execution_id: 'exe_a07e52' }),
      task('tsk_m3t4', 'm3-t4', 'Fix flaky checkout test', 'blocked', [], { execution_id: 'exe_c41d09', state_detail: 'Blocked · last execution failed' }),
      task('tsk_m3t5', 'm3-t5', 'Add receipt screen', 'backlog', ['m3-t3']),
    ],
    harness: harnessBrief('enforce', 2), pending_approval_items: pendingOf('prj_mobile'), requirements: [{ source: 'docs/checkout-flow.md', state: 'unchanged' }], planning: {},
    next_step: { text: 'Decide the Result approval for m3-t2 and the revised workflow v2, then look at the failed m3-t4.', ref_type: 'approval', ref_id: 'apr_m3t2' },
  });
  details.set('prj_docs', {
    ...projects[2]!, phases: [ph('phs_d1', 'P1', 'Content audit', 'awaiting_phase_review'), ph('phs_d2', 'P2', 'Restructure navigation', 'planned')], tasks_phase: 'P1 Content audit',
    tasks: [
      task('tsk_d1t1', 'd1-t1', 'Inventory all pages', 'done', []), task('tsk_d1t2', 'd1-t2', 'Fix broken internal links', 'done', ['d1-t1']),
      task('tsk_d1t3', 'd1-t3', 'Rewrite the install guide', 'done', ['d1-t1']), task('tsk_d1t4', 'd1-t4', 'Audit sidebar order', 'cancelled', []),
    ],
    pending_approval_items: pendingOf('prj_docs'), requirements: [{ source: 'docs/content-plan.md', state: 'unchanged' }], planning: {},
    next_step: { text: 'Review the P1 package and decide Phase Completion. The build validator failed.', ref_type: 'approval', ref_id: 'apr_d1done' },
  });
  details.set('prj_scratch', { ...projects[3]!, phases: [], tasks: [], pending_approval_items: [], requirements: [], planning: {} });

  const req = (id: string, source: string, kind: 'file' | 'text', rh: string | undefined, now_: string | undefined, min: number, state: 'unchanged' | 'changed', used?: string): Requirement =>
    ({ id, source, kind, recorded_hash: rh, hash_now: now_, added_at: at(min), state, used_by: used, version_id: used ? 'wfv_b2' : undefined });
  const requirements = new Map<string, Requirement[]>();
  const wfOf: Record<string, string> = { prj_billing: 'wfv_b2', prj_mobile: 'wfv_m1', prj_docs: 'wfv_d1' };
  requirements.set('prj_billing', [
    req('req_1', 'docs/spec-billing.md', 'file', '9c4e1fa2…b81d', 'e77a03c9…02fe', 3000, 'changed', 'Workflow billing-api v2 (approved)'),
    req('req_2', 'docs/api-contracts.md', 'file', '51d0b7e3…44c9', '51d0b7e3…44c9', 3000, 'unchanged', 'Workflow billing-api v2 (approved)'),
    req('req_3', 'Inline: retry policy for failed charges', 'text', undefined, undefined, 1500, 'unchanged', 'Workflow billing-api v2 (approved)'),
  ]);
  requirements.set('prj_mobile', [req('req_4', 'docs/checkout-flow.md', 'file', '0e51…aa12', '0e51…aa12', 4000, 'unchanged', 'Workflow mobile-checkout v1 (approved)')]);
  requirements.set('prj_docs', [req('req_5', 'docs/content-plan.md', 'file', '88f2…1d30', '88f2…1d30', 5000, 'unchanged', 'Workflow docs-site v1 (approved)')]);
  requirements.set('prj_scratch', []);

  for (const [pid, list] of requirements) for (const r of list) if (r.used_by) r.version_id = wfOf[pid];
  for (const [pid, list] of requirements) details.get(pid)!.requirements = list.map((r) => ({ source: r.source, state: r.state })); // the project's counts follow the list
  const harnesses: Harness[] = [{
    id: 'hrn_claude', name: 'Claude Code', version: '2.1.318', installed: true, connected_projects: ['billing-api', 'mobile-checkout'], available_for_execution: true,
    capabilities: [
      { name: 'launch_execution', value: 'supported', note: 'Headless claude -p' }, { name: 'receive_events', value: 'supported', note: 'stream-json' },
      { name: 'interrupt', value: 'partial', note: 'Clean on SIGINT; a killed session can leave tool processes' }, { name: 'resume_session', value: 'supported', note: 'Same session id' },
      { name: 'connect_mcp', value: 'supported', note: 'stdio and HTTP' }, { name: 'guard_hooks', value: 'partial', note: 'Blocks on deny only; needs the fail-closed wrapper' },
      { name: 'structured_output', value: 'unknown', note: 'Not observed' },
    ],
  }];
  const guard = new Map<string, S['ContractGuardInfo']>();
  guard.set('prj_billing', { policy: 'audit', installed: true, would_deny_24h: 2, not_defended: NOT_DEFENDED, unmanaged_sessions: 'allow' });
  guard.set('prj_mobile', { policy: 'enforce', installed: true, would_deny_24h: 2, not_defended: NOT_DEFENDED, unmanaged_sessions: 'allow' });
  guard.set('prj_docs', { policy: 'off', installed: false, would_deny_24h: 0, not_defended: NOT_DEFENDED, unmanaged_sessions: 'allow' });
  guard.set('prj_scratch', { policy: 'off', installed: false, would_deny_24h: 0, not_defended: NOT_DEFENDED, unmanaged_sessions: 'allow' });
  const guardDecisions = new Map<string, GuardDecision[]>();
  guardDecisions.set('prj_billing', [
    { at: at(3), tool: 'Write', target: 'internal/billing/legacy_export.go', decision: 'allow', would_deny: true, reason: 'unmanaged' },
    { at: at(11), tool: 'Bash', target: 'go test ./...', decision: 'allow', would_deny: false, reason: 'bash_unchecked' },
    { at: at(32), tool: 'Edit', target: 'go.mod', decision: 'allow', would_deny: true, reason: 'outside_workspace' },
  ]);
  guardDecisions.set('prj_mobile', [
    { at: at(60), tool: 'Write', target: 'ios/Podfile', decision: 'deny', would_deny: true, reason: 'outside_workspace' },
    { at: at(61), tool: 'Edit', target: 'package.json', decision: 'deny', would_deny: true, reason: 'unmanaged' },
  ]);
  for (const id of ['prj_docs', 'prj_scratch']) guardDecisions.set(id, []);

  const rec = (seq: number, min: number, event: string, actor: AuditRecord['actor_scope'], subject: string, detail: string): AuditRecord => ({ seq, at: at(min), event, actor_scope: actor, subject, detail });
  const audit: AuditRecord[] = [
    rec(1284, 0, 'task.blocked', 'system', 'p2-t3', 'recovery_required'), rec(1283, 3, 'execution.disconnected', 'system', 'exe_8f3a1c', '3 missed heartbeats'),
    rec(1282, 12, 'approval.requested', 'system', 'docs-site P1 Phase Completion', 'category phase_completion'), rec(1281, 27, 'approval.requested', 'system', 'mobile-checkout workflow v2', 'category workflow'),
    rec(1280, 41, 'approval.requested', 'agent', 'm3-t2 Result', 'category result'), rec(1279, 60, 'guard.decision', 'guard', 'mobile-checkout', '2 denied (enforce)'),
    rec(1278, 230, 'approval.approved', 'human', 'billing-api P2 Phase Start', 'passphrase · web · single use'), rec(1277, 230, 'stepup.succeeded', 'human', 'session 7f2c', 'single use'),
    rec(1276, 250, 'approval.approved', 'human', 'billing-api P1 Phase Completion', 'passphrase · CLI · TTY'), rec(1275, 250, 'stepup.succeeded', 'human', 'session 7f2c', 'single use'),
    rec(1274, 140, 'auth.wrong_scope', 'operator', 'POST /approvals/apr_1/decision', 'rejected 403, audited'),
  ];

  const settings: Settings = {
    passphrase_set: true, elevation_window_min: 5, listen: '127.0.0.1:7421', lan_enabled: false, max_concurrent: 2, hard_cap: 8, heartbeat_interval_s: 15,
    log_retention_days: 30, log_max_mb: 10, session_ttl_h: 12, last_backup_at: at(60 * 3), last_backup_bytes: 2_100_000,
  };

  return {
    now, signedIn: true, passphraseSet: true, chainBroken: false, resetAlert: false, elevations: new Map(), failures: 0, lockedUntil: 0, projects, details, requirements, workflows, versions,
    reviews, approvals, executions, logs, diffs, contexts, harnesses, guard, guardDecisions, audit, settings, seq: 1284,
  };
}
