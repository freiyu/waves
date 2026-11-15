import { app } from './app.svelte';
import { resource } from './resource.svelte';
import type { WaitingItem } from '../api/ext';

/** Execution id to the time its harness started waiting for the human, from executions the page already holds. */
export const waitingOf = (items: Array<{ id: string; waiting_since?: string | null }>) =>
  new Map(items.filter((x) => x.waiting_since).map((x) => [x.id, x.waiting_since!] as [string, string]));

/** Running executions whose harness said its turn ended and waits for the human (waiting_since on the execution). A failure is
 *  not turned into an empty list: `error` is set and the page says so. */
export function waitingResource() {
  const a = app();
  return resource<WaitingItem[]>(async () => [...waitingOf((await a.api.executions()).items)].map(([execution_id, since]) => ({ execution_id, since })), ['execution.changed']);
}

/** Minutes since an ISO time, at least 1. */
export const minutesSince = (iso: string, now = Date.now()) => Math.max(1, Math.round((now - Date.parse(iso)) / 60_000));

/** "Fork of X" text for a project, empty until the project list is known. */
export const forkText = (items: Array<{ id: string; name: string }> | undefined, forkOf?: string | null) =>
  forkOf && items ? `Fork of ${items.find((x) => x.id === forkOf)?.name ?? 'another project'}` : '';

/** How long a wait has lasted, for people: minutes up to an hour and a half, then hours. */
export const waitedFor = (iso: string, now = Date.now()) => {
  const m = minutesSince(iso, now);
  return m < 90 ? `${m} min` : `${Math.round(m / 60)} h`;
};

/** The first execution of a list as "project · task code", and how many more; "none" for an empty list. For the stat cards. */
export const firstAndMore = (xs: Array<{ project_name: string; task_code: string }>) =>
  xs[0] ? `${xs[0].project_name} · task ${xs[0].task_code}${xs.length > 1 ? ` and ${xs.length - 1} more` : ''}` : 'none';
