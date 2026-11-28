<script lang="ts">
  // The passphrase field with everything the service can say about it. Errors are in words, never a colour change alone.
  import Note from './Note.svelte';
  import Icon from './Icon.svelte';
  import type { Attempt } from './attempt.svelte';
  let { attempt, value = $bindable(), needed = true, label, note, nopass, blocked = false, placeholder }: { attempt: Attempt; value: string; needed?: boolean; label: string; note?: string; nopass: string; blocked?: boolean; placeholder?: string } = $props();
  const err = $derived(attempt.err);
  const chainBroken = $derived(err?.code === 'audit_chain_broken' || blocked);
  const lockedOut = $derived(attempt.wait > 0);
  const wrong = $derived(err?.code === 'stepup_failed'); // shown on the field itself, not in a note above it
  const fmt = (s: number) => `${Math.floor(s / 60)} min ${String(s % 60).padStart(2, '0')} s`;
</script>

{#if chainBroken}
  <Note kind="warn" role="alert"><b>Approvals are blocked.</b> The audit chain did not verify{err?.body?.record ? ` at record ${err.body.record.toLocaleString('en')}` : ''}. A human must acknowledge this on the Activity page first.</Note>
{:else if lockedOut}
  <Note kind="dng" role="alert"><b>Too many attempts.</b> Try again in {fmt(attempt.wait)}. The failed attempts were recorded in the audit log.</Note>
{:else if err && !wrong}
  <Note kind="dng" icon="fail" role="alert"><b>{err.message}</b>{#if err.nextStep} {err.nextStep}{/if}</Note>
{/if}
{#if needed}
  <div>
    <label class="lbl" for={attempt.id}>{label}</label>
    {#key attempt.fails}
      <div class="pw" class:bad={wrong}>
        <input id={attempt.id} class="field w-full" type="password" autocomplete="off" spellcheck="false" {placeholder} bind:value disabled={chainBroken || lockedOut || attempt.busy} aria-invalid={wrong || undefined} aria-describedby={wrong ? `${attempt.id}-wrong` : undefined} />
        {#if wrong}<span class="pw-x" aria-hidden="true"><Icon name="fail" size={20} stroke={2} /></span>{/if}
      </div>
    {/key}
    {#if wrong}
      <p id="{attempt.id}-wrong" class="pw-msg" role="alert"><b>Wrong passphrase.</b> {err?.body?.attempts_left ?? 0} {err?.body?.attempts_left === 1 ? 'attempt' : 'attempts'} left before a delay. Nothing was approved.</p>
    {/if}
  </div>
  <div class="muted small">{note ?? 'Sent only to Waves on this machine. Not stored, not logged. Valid for this one action.'}</div>
{:else}
  <Note kind="neu" icon="lock">{nopass}</Note>
{/if}
