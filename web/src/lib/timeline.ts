// The last hour of executions as bars. Pure arithmetic, so it can be tested.
import type { ExecutionSummary } from '../api/models';

export const WINDOW_MS = 3_600_000;

/** The filter groups, in the order of the chips. A disconnected execution counts as interrupted, a starting one as running. */
export const GROUPS = [
  { id: 'running', label: 'Running' }, { id: 'waiting', label: 'Waiting' }, { id: 'waiting_approval', label: 'Needs decision' },
  { id: 'interrupted', label: 'Interrupted' }, { id: 'failed', label: 'Failed' }, { id: 'queued', label: 'Queued' },
  { id: 'paused', label: 'Paused' }, { id: 'completed', label: 'Completed' }, { id: 'cancelled', label: 'Cancelled' },
];

export const groupOf = (x: Pick<ExecutionSummary, 'state' | 'waiting_since'>) =>
  x.waiting_since ? 'waiting' : x.state === 'starting' ? 'running' : x.state === 'disconnected' ? 'interrupted' : x.state;

/** Where the bar of an execution lies in the window, in percent. Null when it never started or ended before the window. */
export function span(x: Pick<ExecutionSummary, 'started_at' | 'ended_at'>, now: number): { left: number; width: number } | null {
  if (!x.started_at) return null;
  const from = now - WINDOW_MS;
  const start = Math.max(from, Date.parse(x.started_at));
  const end = Math.min(now, x.ended_at ? Date.parse(x.ended_at) : now);
  if (end <= from || end < start) return null;
  return { left: ((start - from) / WINDOW_MS) * 100, width: ((end - start) / WINDOW_MS) * 100 };
}

export const inLastHour = (x: Pick<ExecutionSummary, 'started_at' | 'ended_at'>, now: number) => span(x, now) !== null;
