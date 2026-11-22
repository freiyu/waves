<script lang="ts">
  // One pending approval in full: what it asks for, what it approves, the commands it fixes, and the decision controls. It is shown
  // in the Plan pane for a workflow that waits. A phase completion is decided in the Review pane, where the validator results are
  // in front of the human; a result or a risky action is decided from an icon, whose confirmation lists every fact it carries.
  import { visible } from './text';
  import { app } from './app.svelte';
  import { resource } from './resource.svelte';
  import { ago } from './format';
  import { label } from './status';
  import Badge from './Badge.svelte';
  import DecisionBox from './DecisionBox.svelte';
  import Note from './Note.svelte';
  import View from './View.svelte';

  let { id, ondecided, commands = true }: { id: string; ondecided?: () => void; commands?: boolean } = $props();
  const a = app();
  const res = resource(() => a.api.approval(id), ['approval.changed']);
</script>

<View {res} label="the approval">
  {#snippet children(d)}
    <section class="panel" aria-label="{d.category_label} · {d.project_name}">
      <div class="ph"><Badge state="awaiting_approval" text={d.category_label} /><span class="muted">requested {ago(d.requested_at)}</span></div>
      <div class="pb stack">
        <h3 class="text-base font-semibold">{visible(d.subject)}</h3>
        <dl class="kv">
          <dt>Action</dt><dd>{visible(d.action)}</dd><dt>Project</dt><dd>{d.project_name}</dd><dt>Requested by</dt><dd>{d.requested_by}</dd>
          {#each d.facts as f (f.label)}<dt>{f.label}</dt><dd class:mono={f.mono}>{visible(f.value)}</dd>{/each}
        </dl>
      </div>
      <details class="disc" open><summary>What this approves</summary><div class="pb">{visible(d.impact)}</div></details>
      {#if d.will_cancel?.length}
        <details class="disc" open><summary>Work this cancels <span class="count">{d.will_cancel.length}</span></summary><div class="pb">
          <div class="mb-2">Approving replaces the approved version at once. Cancelled tasks are not rerun automatically.</div>
          <ul class="list-disc pl-5">{#each d.will_cancel as w (w.task_code)}<li><b>{w.task_code}</b>{#if w.title} {visible(w.title)}{/if}{#if w.execution_id} · running as <span class="mono">{w.execution_id}</span>{/if}</li>{/each}</ul>
        </div></details>
      {/if}
      {#if commands && d.commands.length}
        <details class="disc" open><summary>Commands <span class="count">{d.commands.length}</span></summary><div class="pb"><div class="small muted mb-2">Commands this approval fixes:</div><pre class="cli">{visible(d.commands.join('\n'))}</pre></div></details>
      {/if}
      <div class="pb stack foot">
        {#if d.state !== 'pending'}
          <Note kind={d.state === 'approved' ? 'ok' : 'dng'}>Decided: <b>{label(d.state)}</b>{d.decided_at ? ' ' + ago(d.decided_at) : ''}.</Note>
        {:else}
          <DecisionBox approval={d} ondecided={() => { void res.reload(); ondecided?.(); }} />
        {/if}
      </div>
    </section>
  {/snippet}
</View>
