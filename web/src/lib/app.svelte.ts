// Application state shared by every page: the typed client, the session, the
// in-memory elevation, and the event stream. One instance per page load.
import { createClient, ApiError } from '../api/client';
import { API_BASE, type Transport } from '../api/transport';
import type { Session, StepUpResponse, ApprovalDetail } from '../api/models';
import { createEventsClient, type ConnState, type EventName, type EventSourceLike } from './events';

export interface AppDeps {
  transport: Transport;
  eventSource: (url: string) => EventSourceLike;
  staleAfterMs?: number;
}

type Listener = (name: EventName, data: unknown) => void;

export function createApp(deps: AppDeps) {
  const state = $state({
    csrf: null as string | null,
    session: null as Session | null,
    loaded: false,
    signedOut: false,
    conn: 'connecting' as ConnState,
    resync: 0,
    /** Elevation secret lives here and nowhere else: never in storage, URLs, or logs. */
    elevation: null as null | { secret: string; kind: 'window' | 'single_use'; expiresAt: number; approvalId?: string },
  });
  const listeners = new Set<Listener>();
  let events: { close(): void } | undefined;
  let clearTimer: ReturnType<typeof setTimeout> | undefined;

  const api = createClient(deps.transport, {
    get csrf() {
      return state.csrf;
    },
    onUnauthenticated() {
      state.signedOut = true;
      state.session = null;
      state.elevation = null;
      events?.close();
      events = undefined;
    },
  });

  function startEvents() {
    events?.close();
    events = createEventsClient({
      url: API_BASE + '/events',
      factory: deps.eventSource,
      staleAfterMs: deps.staleAfterMs,
      onEvent: (name, data) => listeners.forEach((l) => l(name, data)),
      onState: (s) => (state.conn = s),
      onResync: () => state.resync++,
      // A 401 here runs onUnauthenticated: the signed-out page shows and the stream stays closed.
      shouldRetry: () => api.session().then(() => true, (e) => !(e instanceof ApiError && e.status === 401)),
    });
  }

  async function init() {
    try {
      const s = await api.session();
      state.session = s;
      state.csrf = s.csrf_token;
      state.signedOut = false;
      adoptElevation(s);
      startEvents();
    } catch (e) {
      state.signedOut = e instanceof ApiError && e.status === 401;
    } finally {
      state.loaded = true;
    }
  }

  /** The server may report a window elevation, but only this tab's step-up gives us the secret. */
  function adoptElevation(s: Session) {
    if (!s.elevation && state.elevation?.kind === 'window') state.elevation = null;
  }

  async function signIn(code: string) {
    const s = await api.consumeLink(code);
    state.session = s;
    state.csrf = s.csrf_token;
    state.signedOut = false;
    startEvents();
  }

  async function signOut() {
    await api.logout();
    state.signedOut = true;
    state.session = null;
    state.elevation = null;
    events?.close();
    events = undefined;
  }

  /** Passphrase step-up. With an approval id the result is single use and bound to it; without, a time window. */
  async function stepUp(passphrase: string, approvalId?: string): Promise<StepUpResponse> {
    const r = await api.stepUp(passphrase, approvalId);
    clearTimeout(clearTimer);
    state.elevation = { secret: r.elevation_secret, kind: r.kind, expiresAt: Date.parse(r.expires_at), approvalId: r.approval_id };
    clearTimer = setTimeout(() => (state.elevation = null), Math.max(0, state.elevation.expiresAt - Date.now()));
    return r;
  }

  function dropElevation() {
    clearTimeout(clearTimer);
    const secret = state.elevation?.secret;
    state.elevation = null;
    // Tell the service too, so the confirmation is gone there as well and not only in this tab.
    if (secret) void api.revokeElevation(secret).catch(() => {});
  }

  /** The secret to send for this action, if one is held and covers it. Never asks the server. */
  function elevationFor(a?: Pick<ApprovalDetail, 'id' | 'step_up'>): string | undefined {
    const e = state.elevation;
    if (!e || e.expiresAt < Date.now()) return undefined;
    if (!a) return e.kind === 'window' ? e.secret : undefined;
    if (e.kind === 'single_use') return e.approvalId === a.id ? e.secret : undefined;
    return a.step_up === 'window_ok' ? e.secret : undefined;
  }

  /**
   * Run `fn` with an elevation secret. Uses a held one when it covers the action;
   * otherwise steps up with the passphrase, and forgets the window again afterwards
   * so a one-off change does not leave the tab unlocked.
   */
  async function runElevated<T>(passphrase: string, fn: (secret: string) => Promise<T>): Promise<T> {
    const held = elevationFor();
    if (held) return fn(held);
    await stepUp(passphrase);
    try {
      return await fn(elevationFor()!);
    } finally {
      dropElevation();
    }
  }

  function subscribe(names: EventName[], fn: (name: EventName, data: unknown) => void) {
    const l: Listener = (n, d) => names.includes(n) && fn(n, d);
    listeners.add(l);
    return () => void listeners.delete(l);
  }

  return {
    state, api, init, signIn, signOut, stepUp, dropElevation, elevationFor, runElevated, subscribe,
    close: () => {
      events?.close();
      clearTimeout(clearTimer);
    },
  };
}

export type App = ReturnType<typeof createApp>;

let current: App | undefined;
export const setApp = (a: App) => (current = a);
export const app = (): App => {
  if (!current) throw new Error('app not initialised');
  return current;
};
