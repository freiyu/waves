<script lang="ts">
  import { toast } from 'svelte-sonner';
  import Modal from '../lib/Modal.svelte';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import { ApiError } from '../api/client';
  import type { Requirement } from '../api/models';
  import Icon from '../lib/Icon.svelte';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import View from '../lib/View.svelte';

  const a = app();
  const pid = $derived(route.params.id!);
  const res = resource(() => a.api.requirements(pid), ['project.changed', 'workflow.changed']);
  let pick = $state<string | undefined>();
  let adding = $state(false);
  let kind = $state<'file' | 'text'>('file');
  let path = $state('');
  let text = $state('');
  let error = $state('');

  async function add(e: Event) {
    e.preventDefault();
    error = '';
    try {
      await a.api.addRequirement(pid, kind === 'file' ? { kind, path: path.trim() } : { kind, text: text.trim() });
      toast.success('Requirement added');
      adding = false;
      path = text = '';
      await res.reload();
    } catch (x) {
      error = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
  let versionError = $state('');
  async function newVersion(r: Requirement) {
    if (!r.version_id) return;
    versionError = '';
    try {
      const v = await a.api.newWorkflowVersion(r.version_id);
      toast.success('New workflow version created');
      go('/workflow/' + v.id);
    } catch (x) {
      versionError = x instanceof ApiError ? `${x.message} ${x.nextStep}` : String(x);
    }
  }
</script>

<ProjectHead id={pid} current="requirements">
  {#snippet actions()}<Btn kind="pri" onclick={() => (adding = true)}><Icon name="plus" size={14} />Add requirement</Btn>{/snippet}
</ProjectHead>
<View {res} label="requirements">
  {#snippet children(d)}
    {@const sel = d.items.find((x) => x.id === pick) ?? d.items.find((x) => x.state === 'changed') ?? d.items[0]}
    <div class="cols">
      <Panel title="Sources" count={d.items.length}>
        <ul class="items" aria-label="Requirement sources">
          {#each d.items as r (r.id)}
            <li>
              <button type="button" class="li" class:sel={r.id === sel?.id} aria-current={r.id === sel?.id ? 'true' : undefined} onclick={() => (pick = r.id)}>
                <span class="t"><b class={r.kind === 'file' ? 'mono' : ''}>{r.source}</b><span class="muted small">{r.kind} · {r.recorded_hash?.slice(0, 8) ?? 'no hash'} · added {ago(r.added_at)}</span></span>
                <Badge state={r.state === 'changed' ? 'stale' : 'approved'} text={r.state === 'changed' ? 'Changed since approval' : 'Unchanged'} />
              </button>
            </li>
          {:else}
            <li class="empty">No requirement yet. Add a file reference or write one inline.</li>
          {/each}
        </ul>
      </Panel>
      {#if sel}
        <Panel title={sel.source}>
          <div class="pb">
            <dl class="kv">
              {#if sel.kind === 'file'}
                <dt>Path</dt><dd class="mono">{sel.source}</dd><dt>Recorded hash</dt><dd class="mono">{sel.recorded_hash}</dd><dt>Hash now</dt><dd class="mono">{sel.hash_now}</dd>
              {/if}
              <dt>Used by</dt><dd>{sel.used_by ?? 'No workflow yet'}</dd>
            </dl>
            {#if sel.state === 'changed'}
              <div class="mt-4"><Note kind="warn"><b>The workflow may be stale.</b> The file changed after approval. Waves does not rewrite the workflow. Create a new version to take the change in.</Note></div>
              {#if versionError}<div class="mt-4"><Note kind="dng" role="alert">{versionError}</Note></div>{/if}
              <div class="row wrap mt-4"><Btn disabled={!sel.version_id} onclick={() => newVersion(sel)}>Create new workflow version</Btn></div>
            {/if}
          </div>
        </Panel>
      {/if}
    </div>
    <div class="muted">Files are referenced by path and hash, never overwritten.</div>
  {/snippet}
</View>

{#if adding}
  <Modal labelledby="rq-title" oncancel={() => (adding = false)}>
    <form onsubmit={add}>
      <div class="mh"><h2 id="rq-title">Add a requirement</h2></div>
      <div class="mb">
        <fieldset class="row border-0 p-0"><legend class="lbl">Kind</legend>
          <label><input type="radio" bind:group={kind} value="file" /> File reference</label>
          <label><input type="radio" bind:group={kind} value="text" /> Inline text</label>
        </fieldset>
        {#if kind === 'file'}
          <div><label class="lbl" for="rq-path">Path inside the project</label><input id="rq-path" class="field mono w-full" bind:value={path} placeholder="docs/spec.md" /></div>
        {:else}
          <div><label class="lbl" for="rq-text">Requirement</label><textarea id="rq-text" class="field" bind:value={text}></textarea></div>
        {/if}
        {#if error}<Note kind="dng" icon="fail" role="alert">{error}</Note>{/if}
      </div>
      <div class="mf"><Btn onclick={() => (adding = false)}>Cancel</Btn><Btn type="submit" kind="pri" disabled={kind === 'file' ? !path.trim() : !text.trim()}>Add</Btn></div>
    </form>
  </Modal>
{/if}
