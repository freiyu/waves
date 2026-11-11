<script lang="ts">
  import Modal from './Modal.svelte';
  // The passphrase prompt. It is a separate, deliberate step: nothing here
  // approves anything by itself, it only collects the passphrase and hands it to
  // `onconfirm`, which does the step-up and the action. Errors from the service
  // are shown here in words, never as a color change alone.
  import type { Snippet } from 'svelte';
  import { onDestroy, tick } from 'svelte';
  import { ApiError } from '../api/client';
  import Note from './Note.svelte';
  import Btn from './Btn.svelte';

  interface Props {
    title: string;
    kicker?: string;
    facts?: Array<{ label: string; value: string; mono?: boolean }>;
    extra?: Snippet;
    confirm: string;
    danger?: boolean;
    /** False when a held elevation already covers the action. */
    needPassphrase?: boolean;
    note?: string;
    blocked?: boolean;
    /** The action cannot be confirmed yet (a required field is empty). */
    incomplete?: boolean;
    nopass?: string;
    /** Field label; "Passphrase" for actions that are not approvals. */
    fieldLabel?: string;
    onconfirm: (passphrase: string) => Promise<void>;
    oncancel: () => void;
  }
  let { title, kicker = 'Approval requires your passphrase', facts = [], extra, confirm, danger = false, needPassphrase = true, note, blocked = false, incomplete = false, nopass = 'Your unlocked window already covers this action. No passphrase is asked.', fieldLabel = 'Approval passphrase', onconfirm, oncancel }: Props = $props();

  let pass = $state('');
  let busy = $state(false);
  let err = $state<ApiError | undefined>();
  let wait = $state(0); // seconds left of a rate limit
  let timer: ReturnType<typeof setInterval> | undefined;

  onDestroy(() => clearInterval(timer));

  function startWait(s: number) {
    wait = s;
    clearInterval(timer);
    timer = setInterval(() => {
      wait = Math.max(0, wait - 1);
      if (wait === 0) {
        clearInterval(timer);
        if (err?.code === 'rate_limited') err = undefined; // the wait is over; do not keep saying "try again in"
      }
    }, 1000);
  }

  const chainBroken = $derived(err?.code === 'audit_chain_broken' || blocked);
  const lockedOut = $derived(wait > 0);
  const fmt = (s: number) => `${Math.floor(s / 60)} min ${String(s % 60).padStart(2, '0')} s`;

  async function submit(e: Event) {
    e.preventDefault();
    if (busy || lockedOut || chainBroken) return;
    busy = true;
    err = undefined;
    const typed = pass;
    pass = ''; // the passphrase never stays in the page longer than the call
    try {
      await onconfirm(typed);
    } catch (x) {
      err = x instanceof ApiError ? x : new ApiError(0, 'unknown', String(x));
      if (err.code === 'rate_limited') startWait(err.body?.retry_after_s ?? 30);
    } finally {
      busy = false;
      await tick(); // the field is re-enabled only after this update
      document.getElementById('su-pass')?.focus();
    }
  }
</script>

<Modal labelledby="su-title" {oncancel} locked={busy}>
  <form onsubmit={submit}>
    <div class="mh"><div class="small muted">{kicker}</div><h2 id="su-title">{title}</h2></div>
    <div class="mb">
      {#if facts.length}
        <dl class="kv">{#each facts as f}<dt>{f.label}</dt><dd class:mono={f.mono}>{f.value}</dd>{/each}</dl>
      {/if}
      {@render extra?.()}
      {#if chainBroken}
        <Note kind="warn" role="alert"><b>Approvals are blocked.</b> The audit chain did not verify{err?.body?.record ? ` at record ${err.body.record.toLocaleString('en')}` : ''}. A human must acknowledge this on the Activity page first.</Note>
      {:else if lockedOut}
        <Note kind="dng" role="alert"><b>Too many attempts.</b> Try again in {fmt(wait)}. The failed attempts were recorded in the audit log.</Note>
      {:else if err?.code === 'stepup_failed'}
        <Note kind="dng" icon="fail" role="alert"><b>Wrong passphrase.</b> {err.body?.attempts_left ?? 0} {err.body?.attempts_left === 1 ? 'attempt' : 'attempts'} left before a delay. Nothing was approved.</Note>
      {:else if err}
        <Note kind="dng" icon="fail" role="alert"><b>{err.message}</b>{#if err.nextStep} {err.nextStep}{/if}</Note>
      {/if}
      {#if needPassphrase}
        <div>
          <label class="lbl" for="su-pass">{fieldLabel}</label>
          <input id="su-pass" class="field w-full min-h-9" type="password" autocomplete="off" spellcheck="false" bind:value={pass} disabled={chainBroken || lockedOut || busy} />
        </div>
        <div class="muted">{note ?? 'Typed into this page only; not stored, not logged. Valid for this one action.'}</div>
      {:else}
        <Note kind="neu" icon="lock">{nopass}</Note>
      {/if}
    </div>
    <div class="mf">
      <Btn disabled={busy} onclick={oncancel}>Cancel</Btn>
      <Btn type="submit" kind={danger ? 'dng' : 'pri'} lock disabled={busy || incomplete || lockedOut || chainBroken || (needPassphrase && pass === '')}>{busy ? 'Working…' : confirm}</Btn>
    </div>
  </form>
</Modal>
