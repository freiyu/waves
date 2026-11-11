<script lang="ts">
  import Modal from './Modal.svelte';
  // A plain confirmation for consequential actions that need no passphrase.
  import type { Snippet } from 'svelte';
  import { ApiError } from '../api/client';
  import Btn from './Btn.svelte';
  import Note from './Note.svelte';
  let { title, confirm, danger = false, onconfirm, oncancel, children }: { title: string; confirm: string; danger?: boolean; onconfirm: () => Promise<void>; oncancel: () => void; children: Snippet } = $props();
  let busy = $state(false);
  let err = $state('');
  async function go(e: Event) {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      await onconfirm();
    } catch (x) {
      err = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    } finally {
      busy = false;
    }
  }
</script>

<Modal labelledby="cf-title" {oncancel} locked={busy}>
  <form onsubmit={go}>
    <div class="mh"><h2 id="cf-title">{title}</h2></div>
    <div class="mb">{@render children()}{#if err}<Note kind="dng" icon="fail" role="alert">{err}</Note>{/if}</div>
    <div class="mf"><Btn disabled={busy} onclick={oncancel}>Cancel</Btn><Btn type="submit" kind={danger ? 'dng' : 'pri'} disabled={busy}>{busy ? 'Working…' : confirm}</Btn></div>
  </form>
</Modal>
