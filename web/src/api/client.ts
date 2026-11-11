import type { paths } from './types';
import type { ErrorBody } from './models';
import type { Method, Transport } from './transport';

type Ok = 200 | 201 | 202 | 204;
type Body<R> = R extends { content: { 'application/json': infer B } }
  ? B
  : R extends { content: { 'text/plain': infer B } }
    ? B
    : void;
type OkResponse<O> = O extends { responses: infer R } ? Body<R[Extract<keyof R, Ok>]> : never;
type ReqBody<O> = O extends { requestBody?: { content: { 'application/json': infer B } } } ? B : never;
type Query<O> = O extends { parameters: { query?: infer Q } } ? NonNullable<Q> : never;

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public nextStep = '',
    public body?: ErrorBody['error'],
  ) {
    super(message);
  }
}

export interface Session {
  csrf: string | null;
  /** Called when the service answers 401: the session is gone. */
  onUnauthenticated: () => void;
}

export interface CallOptions {
  path?: Record<string, string>;
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  /** Elevation secret for routes that need `human` scope. Kept in memory only. */
  elevation?: string;
}

/** The only place that builds requests: CSRF, JSON, error mapping, 401 handling. */
export function createClient(transport: Transport, session: Session) {
  async function raw(method: Method, path: string, opts: CallOptions = {}): Promise<string> {
    for (const v of Object.values(opts.path ?? {})) {
      // "." and ".." would be resolved by the browser and walk out of the intended route.
      if (v === '' || v === '.' || v === '..') throw new ApiError(0, 'invalid_id', 'That address is not valid.', 'Go back to the list and open the item again.');
    }
    let url = path.replace(/\{(\w+)\}/g, (_, k: string) => encodeURIComponent(opts.path?.[k] ?? ''));
    const q = Object.entries(opts.query ?? {}).filter(([, v]) => v !== undefined && v !== '');
    if (q.length) url += '?' + new URLSearchParams(q.map(([k, v]) => [k, String(v)])).toString();
    const headers: Record<string, string> = { Accept: 'application/json, text/plain' };
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
    if (method !== 'get' && session.csrf) headers['X-Waves-CSRF'] = session.csrf;
    if (opts.elevation) headers['X-Waves-Elevation'] = opts.elevation;
    let res;
    try {
      res = await transport({ method, url, headers, body: opts.body === undefined ? undefined : JSON.stringify(opts.body) });
    } catch (e) {
      throw new ApiError(0, 'unreachable', 'The Waves service did not answer.', 'Check that it is running: waves status.', undefined);
    }
    if (res.status === 401 && !url.startsWith('/auth/')) session.onUnauthenticated();
    if (res.status >= 400) {
      let eb: ErrorBody['error'] | undefined;
      try {
        eb = (JSON.parse(res.text) as ErrorBody).error;
      } catch {
        /* not JSON: keep the generic message */
      }
      throw new ApiError(res.status, eb?.code ?? 'http_' + res.status, eb?.message ?? `The service answered ${res.status}.`, eb?.next_step ?? '', eb);
    }
    return res.text;
  }

  async function call<P extends keyof paths, M extends keyof paths[P] & Method>(
    method: M,
    path: P,
    opts: CallOptions = {},
  ): Promise<OkResponse<paths[P][M]>> {
    const text = await raw(method, path as string, opts);
    return (text === '' ? undefined : JSON.parse(text)) as OkResponse<paths[P][M]>;
  }

  const text = (path: string, opts: CallOptions) => raw('get', path, opts);

  type Q<P extends keyof paths> = Query<paths[P] extends { get: infer G } ? G : never>;
  const list = <P extends keyof paths>(q?: Q<P>) => ({ query: q as CallOptions['query'] });

  return {
    health: () => call('get', '/health'),
    status: () => call('get', '/status'),
    linkStatus: (code: string) => call('get', '/auth/link/{code}', { path: { code } }),
    consumeLink: (code: string) => call('post', '/auth/consume', { body: { code } satisfies ReqBody<paths['/auth/consume']['post']> }),
    session: () => call('get', '/auth/session'),
    logout: () => call('post', '/auth/logout'),
    stepUp: (passphrase: string, approvalId?: string) =>
      call('post', '/auth/stepup', { body: { passphrase, approval_id: approvalId } satisfies ReqBody<paths['/auth/stepup']['post']> }),
    audit: (cursor?: string) => call('get', '/audit', list<'/audit'>({ limit: 50, cursor })),
    acknowledgeAudit: (elevation: string) => call('post', '/audit/acknowledge', { elevation }),
    backup: () => call('post', '/backup'),

    projects: () => call('get', '/projects', list<'/projects'>({ limit: 200 })),
    registerProject: (path: string, name?: string) => call('post', '/projects', { body: { path, name } }),
    project: (id: string) => call('get', '/projects/{id}', { path: { id } }),
    activateProject: (id: string) => call('post', '/projects/{id}/activate', { path: { id } }),
    settings: () => call('get', '/settings'),
    patchSettings: (body: ReqBody<paths['/settings']['patch']>, elevation?: string) => call('patch', '/settings', { body, elevation }),

    requirements: (id: string) => call('get', '/projects/{id}/requirements', { path: { id } }),
    addRequirement: (id: string, body: ReqBody<paths['/projects/{id}/requirements']['post']>) =>
      call('post', '/projects/{id}/requirements', { path: { id }, body }),
    requestPlanning: (id: string) => call('post', '/projects/{id}/planning-requests', { path: { id } }),
    workflow: (id: string) => call('get', '/projects/{id}/workflow', { path: { id } }),
    workflowVersion: (id: string) => call('get', '/workflow-versions/{id}', { path: { id } }),
    newWorkflowVersion: (id: string) => call('post', '/workflow-versions/{id}/versions', { path: { id } }),
    submitWorkflow: (id: string) => call('post', '/workflow-versions/{id}/submit', { path: { id } }),
    phaseReview: (id: string) => call('get', '/phases/{id}/review', { path: { id } }),
    reopenTasks: (id: string, taskIds: string[], reason: string, elevation: string) =>
      call('post', '/phases/{id}/reopen', { path: { id }, body: { task_ids: taskIds, reason }, elevation }),
    confirmValidation: (id: string, vid: string, index: number, elevation: string) =>
      call('post', '/phases/{id}/validation-runs/{vid}/confirm', { path: { id, vid }, body: { index }, elevation }),

    approvals: (state?: 'pending' | 'approved' | 'rejected' | 'cancelled') => call('get', '/approvals', list<'/approvals'>({ limit: 200, state })),
    approval: (id: string) => call('get', '/approvals/{id}', { path: { id } }),
    decide: (id: string, body: ReqBody<paths['/approvals/{id}/decision']['post']>, elevation?: string) =>
      call('post', '/approvals/{id}/decision', { path: { id }, body, elevation }),

    executions: () => call('get', '/executions', list<'/executions'>({ limit: 200 })),
    execution: (id: string) => call('get', '/executions/{id}', { path: { id } }),
    log: (id: string, offset: number) => text('/executions/{id}/log'.replace('{id}', encodeURIComponent(id)), { query: { offset } }),
    diff: (id: string) => text('/executions/{id}/diff'.replace('{id}', encodeURIComponent(id)), {}),
    context: (id: string) => call('get', '/executions/{id}/context', { path: { id } }),
    interrupt: (id: string) => call('post', '/executions/{id}/cancel', { path: { id } }),
    resolve: (id: string, decision: ReqBody<paths['/executions/{id}/resolve']['post']>['decision']) =>
      call('post', '/executions/{id}/resolve', { path: { id }, body: { decision } }),
    stopOrphans: (id: string) => call('post', '/executions/{id}/orphans/stop', { path: { id } }),

    harnesses: () => call('get', '/harnesses'),
    guard: (id: string) => call('get', '/projects/{id}/guard', { path: { id } }),
    guardDecisions: (id: string) => call('get', '/projects/{id}/guard/decisions', { path: { id } }),
    patchProjectSettings: (id: string, body: ReqBody<paths['/projects/{id}/settings']['patch']>, elevation?: string) =>
      call('patch', '/projects/{id}/settings', { path: { id }, body, elevation }),
    disconnect: (id: string, elevation?: string) => call('post', '/projects/{id}/agent/disconnect', { path: { id }, elevation }),
    connectPlan: (id: string) => call('get', '/projects/{id}/agent/connect-plan', { path: { id } }),
    connect: (id: string, planHash: string, elevation?: string) =>
      call('post', '/projects/{id}/agent/connect', { path: { id }, body: { plan_hash: planHash }, elevation }),
    revokeElevation: (secret: string) => call('post', '/auth/stepup/revoke', { elevation: secret }),
  };
}

export type Api = ReturnType<typeof createClient>;
