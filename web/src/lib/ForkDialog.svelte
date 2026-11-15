<script lang="ts">
  // Mark a project as a fork of another registered project, or clear the mark. A label for the Graph: it changes no rule of
  // approval, leases or the guard, so it asks for no passphrase.
  import { toast } from 'svelte-sonner';
  import { app } from './app.svelte';
  import { ApiError } from '../api/client';
  import type { Project } from '../api/models';
  import Modal from './Modal.svelte';
  import Note from './Note.svelte';
  import Btn from './Btn.svelte';

  let { project, others, ondone, oncancel }: { project: { id: string; name: string; fork_of?: string | null }; others: Project[]; ondone: () => void; oncancel: () => void } = $props();
  const a = app();
  // A project that is a fork of this one cannot be its original (the service refuses a loop, so do not offer it).
  const offered = $derived.by(() => {
    const below = new Set<string>();
    for (let grew = true; grew; ) {
      grew = false;
      for (const o of others) if (o.fork_of && (o.fork_of === project.id || below.has(o.fork_of)) && !below.has(o.id)) { below.add(o.id); grew = true; }
    }
    return others.filter((o) => o.id !== project.id && !below.has(o.id));
  });
  // svelte-ignore state_referenced_locally
  let pick = $state(project.fork_of ?? '');
  let busy = $state(false);
  let error = $state('');
  const changed = $derived(pick !== (project.fork_of ?? ''));

  async function save(e: Event) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await a.api.x.setFork(project.id, pick);
      const origin = others.find((o) => o.id === pick)?.name;
      toast.success(pick ? `${project.name} is now a fork of ${origin ?? 'another project'}` : `${project.name} is no longer marked as a fork`);
      ondone();
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    } finally {
      busy = false;
    }
  }
</script>

<Modal labelledby="fk-title" {oncancel} locked={busy}>
  <form onsubmit={save}>
    <div class="mh"><h2 id="fk-title">{project.fork_of ? `Change the original of ${project.name}` : `Mark ${project.name} as a fork`}</h2></div>
    <div class="mb">
      <div class="muted max-w-[65ch]">Use this when the folder is a copy of another registered project, such as a Git clone. Waves only records the link and draws it on the Graph. It changes no approval, lease or guard rule.</div>
      {#if offered.length || project.fork_of}
        <div>
          <label class="lbl" for="fk-pick">Original project</label>
          <select id="fk-pick" class="field min-h-10 w-full" bind:value={pick}>
            <option value="">None, not a fork</option>
            {#each offered as o (o.id)}<option value={o.id}>{o.name}</option>{/each}
          </select>
        </div>
      {:else}
        <Note kind="neu"><b>No other project to pick.</b> Register the original first, then come back.</Note>
      {/if}
      {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
    </div>
    <div class="mf"><Btn disabled={busy} onclick={oncancel}>Cancel</Btn><Btn type="submit" kind="pri" disabled={busy || !changed}>{busy ? 'Saving…' : 'Save'}</Btn></div>
  </form>
</Modal>
