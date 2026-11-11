// SSE client: one stream, reconnect with backoff, and a connection state the UI
// can show. No polling: the only timers are the reconnect delay and the
// "stale" deadline.
import type { EventPayloads } from '../api/models';

export interface EventSourceLike {
  onopen: (() => void) | null;
  onerror: (() => void) | null;
  readyState: number;
  addEventListener(name: string, fn: (e: { data: string; lastEventId: string }) => void): void;
  close(): void;
}

export type ConnState = 'connecting' | 'live' | 'reconnecting' | 'stale';
export type EventName = keyof EventPayloads;

/** Every event name of the contract. A test compares this list with the generated schema. */
export const EVENT_NAMES: EventName[] = [
  'hello', 'reset', 'project.changed', 'workflow.changed', 'phase.changed', 'task.changed', 'execution.changed', 'execution.log',
  'approval.changed', 'attention.changed', 'harness.changed', 'guard.denial', 'audit.alert',
];

export interface EventsOptions {
  url: string;
  factory: (url: string) => EventSourceLike;
  onEvent: (name: EventName, data: unknown) => void;
  onState: (s: ConnState) => void;
  /** State may be stale (new connection after a loss, or a `reset` event): refetch what is on screen. */
  onResync: () => void;
  staleAfterMs?: number;
  backoffMs?: (attempt: number) => number;
  /**
   * Asked before opening a new stream after a refused one. A stream refused
   * because the session ended gives no status code, so the caller checks the
   * session once; false stops the client for good.
   */
  shouldRetry?: () => Promise<boolean>;
}

const CLOSED = 2;

export function createEventsClient(o: EventsOptions) {
  const staleAfter = o.staleAfterMs ?? 30_000;
  const backoff = o.backoffMs ?? ((n) => Math.min(30_000, 1000 * 2 ** n) * (0.75 + Math.random() * 0.5));
  let src: EventSourceLike | undefined;
  let attempt = 0;
  let lost = false;
  let stopped = false;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let staleTimer: ReturnType<typeof setTimeout> | undefined;

  function markLost() {
    lost = true;
    o.onState('reconnecting');
    staleTimer ??= setTimeout(() => o.onState('stale'), staleAfter);
  }

  function open() {
    if (stopped) return;
    const s = o.factory(o.url);
    src = s;
    s.onopen = () => {
      clearTimeout(staleTimer);
      staleTimer = undefined;
      attempt = 0;
      o.onState('live');
      // A brand new EventSource has no Last-Event-ID: whatever happened meanwhile is unknown.
      if (lost) o.onResync();
      lost = false;
    };
    s.onerror = () => {
      markLost();
      // The browser retries on its own unless the connection was refused for good.
      if (s.readyState === CLOSED && !stopped) {
        clearTimeout(retry);
        retry = setTimeout(async () => {
          if (o.shouldRetry && !(await o.shouldRetry().catch(() => true))) {
            stopped = true;
            clearTimeout(staleTimer);
            return;
          }
          open();
        }, backoff(attempt++));
      }
    };
    for (const name of EVENT_NAMES) {
      s.addEventListener(name, (e) => {
        if (name === 'reset') return o.onResync();
        let data: unknown;
        try {
          data = JSON.parse(e.data);
        } catch {
          return; // a malformed event is dropped, never thrown into the UI
        }
        o.onEvent(name, data);
      });
    }
  }

  open();
  return {
    close() {
      stopped = true;
      clearTimeout(retry);
      clearTimeout(staleTimer);
      src?.close();
    },
  };
}
