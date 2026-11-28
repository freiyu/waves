<script lang="ts">
  import Modal from './Modal.svelte';
  // The passphrase prompt. It is a separate, deliberate step: nothing here
  // approves anything by itself, it only collects the passphrase and hands it to
  // `onconfirm`, which does the step-up and the action. Errors from the service
  // are shown here in words, never as a color change alone.
  import type { Snippet } from 'svelte';
  import { passphraseAttempt } from './attempt.svelte';
  import { showDone } from './done.svelte';
  import PassField from './PassField.svelte';
  import Btn from './Btn.svelte';
  import { visible } from './text';

  interface Props {
    title: string;
    kicker?: string;
    facts?: Array<{ label: string; value: string; mono?: boolean }>;
    extra?: Snippet;
    confirm: string;
    /** The word shown when the service accepted it, for example "Approved". */
    done: string;
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
  let { title, kicker = 'Approval requires your passphrase', facts = [], extra, confirm, done, danger = false, needPassphrase = true, note, blocked = false, incomplete = false, nopass = 'Your unlocked window already covers this action. No passphrase is asked.', fieldLabel = 'Approval passphrase', onconfirm, oncancel }: Props = $props();

  const attempt = passphraseAttempt('su-pass');
  let pass = $state('');
  const busy = $derived(attempt.busy);
  const chainBroken = $derived(attempt.err?.code === 'audit_chain_broken' || blocked);
  const lockedOut = $derived(attempt.wait > 0);

  async function submit(e: Event) {
    e.preventDefault();
    if (busy || lockedOut || chainBroken) return;
    const typed = pass;
    pass = ''; // the passphrase never stays in the page longer than the call
    const [word, bad] = [done, danger]; // read now: on success the caller may change or close the dialog
    if (await attempt.run(() => onconfirm(typed))) showDone(word, bad);
    // The dialog is still open after a success only if the caller keeps it; either way the field takes focus again.
    document.getElementById('su-pass')?.focus();
  }
</script>

<Modal labelledby="su-title" {oncancel} locked={busy}>
  <form onsubmit={submit}>
    <div class="mh"><div class="small muted">{kicker}</div><h2 id="su-title">{title}</h2></div>
    <div class="mb">
      {#if facts.length}
        <dl class="kv">{#each facts as f}<dt>{f.label}</dt><dd class:mono={f.mono}>{visible(f.value)}</dd>{/each}</dl>
      {/if}
      {@render extra?.()}
      <PassField {attempt} bind:value={pass} needed={needPassphrase} label={fieldLabel} {note} {nopass} {blocked} />
    </div>
    <div class="mf">
      <Btn disabled={busy} onclick={oncancel}>Cancel</Btn>
      <Btn type="submit" kind={danger ? 'dng' : 'pri'} lock disabled={busy || incomplete || lockedOut || chainBroken || (needPassphrase && pass === '')}>{busy ? 'Working…' : confirm}</Btn>
    </div>
  </form>
</Modal>
