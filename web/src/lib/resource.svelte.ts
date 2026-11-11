import { onDestroy } from 'svelte';
import { ApiError } from '../api/client';
import { app } from './app.svelte';
import type { EventName } from './events';

/**
 * Load data for a page. `load` is re-run when anything reactive it reads
 * synchronously changes (such as a route parameter), when one of `events` arrives,
 * and after the event stream resynchronises. Reloads keep the old data on screen:
 * only the first load shows a skeleton, so live updates never shift the layout.
 */
export function resource<T>(load: () => Promise<T>, events: EventName[] = []) {
  const s = $state({ data: undefined as T | undefined, error: undefined as ApiError | undefined, loading: true });
  let seq = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  async function run(first: boolean) {
    const mine = ++seq;
    if (first) s.loading = true;
    try {
      const r = await load();
      if (mine !== seq) return;
      s.data = r;
      s.error = undefined;
    } catch (e) {
      if (mine !== seq) return;
      s.error = e instanceof ApiError ? e : new ApiError(0, 'unknown', String(e));
    } finally {
      if (mine === seq) s.loading = false;
    }
  }

  $effect(() => {
    void run(true);
  });

  const a = app();
  const off = events.length
    ? a.subscribe(events, () => {
        clearTimeout(timer);
        timer = setTimeout(() => void run(false), 250); // bursts coalesce into one refetch
      })
    : () => {};
  let seen = a.state.resync;
  $effect(() => {
    const r = a.state.resync;
    if (r !== seen) {
      seen = r;
      void run(false);
    }
  });
  onDestroy(() => {
    off();
    clearTimeout(timer);
  });

  return {
    get data() { return s.data; },
    get error() { return s.error; },
    get loading() { return s.loading; },
    reload: () => run(false),
    /** Replace the data with a fresher copy returned by a write. */
    set: (v: T) => { s.data = v; },
  };
}
