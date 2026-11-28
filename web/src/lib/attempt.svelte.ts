// One passphrase prompt: it hands the typed passphrase to an action exactly once, and keeps what the service said about
// the failure (wrong passphrase, delay, broken audit chain). The passphrase itself is never kept here.
import { onDestroy, tick } from 'svelte';
import { ApiError } from '../api/client';

export function passphraseAttempt(fieldId: string) {
  const s = $state({ busy: false, err: undefined as ApiError | undefined, wait: 0, fails: 0 }); // wait: seconds left of a delay
  let timer: ReturnType<typeof setInterval> | undefined;
  onDestroy(() => clearInterval(timer));

  function startWait(seconds: number) {
    s.wait = seconds;
    clearInterval(timer);
    timer = setInterval(() => {
      s.wait = Math.max(0, s.wait - 1);
      if (s.wait === 0) {
        clearInterval(timer);
        if (s.err?.code === 'rate_limited') s.err = undefined; // the wait is over; do not keep saying "try again in"
      }
    }, 1000);
  }

  return {
    id: fieldId,
    get busy() { return s.busy; },
    get err() { return s.err; },
    get wait() { return s.wait; },
    /** Each failure replays the shake. */
    get fails() { return s.fails; },
    reset: () => { s.err = undefined; },
    /** True only when the action went through. */
    async run(action: () => Promise<void>): Promise<boolean> {
      if (s.busy) return false;
      s.busy = true;
      s.err = undefined;
      let ok = false;
      try {
        await action();
        ok = true;
      } catch (x) {
        s.err = x instanceof ApiError ? x : new ApiError(0, 'unknown', String(x));
        if (s.err.code === 'stepup_failed') s.fails++;
        if (s.err.code === 'rate_limited') startWait(s.err.body?.retry_after_s ?? 30);
      } finally {
        s.busy = false;
        await tick(); // the field is re-enabled only after this update
        if (s.err) document.getElementById(fieldId)?.focus();
      }
      return ok;
    },
  };
}
export type Attempt = ReturnType<typeof passphraseAttempt>;
