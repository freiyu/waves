<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { ago } from '../lib/format';
  import type { TaskSummary, ProjectDetail } from '../api/models';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Btn from '../lib/Btn.svelte';
  import Badge from '../lib/Badge.svelte';
  import View from '../lib/View.svelte';
  import Chips from '../lib/Chips.svelte';
  import { flashOnChange } from '../lib/flash';
  import IconBtn from '../lib/IconBtn.svelte';
  import { visible } from '../lib/text';
  import DecideFlow from '../lib/DecideFlow.svelte';
  import { shellData } from '../lib/shell';
  import Icon from '../lib/Icon.svelte';
  import ProjectHead from '../lib/ProjectHead.svelte';

  import { status } from '../lib/status';
  import RowMenu from '../lib/RowMenu.svelte';
  import { copyText } from '../lib/copy';

  const a = app();
  const res = resource(() => a.api.project(route.params.id!), ['project.changed', 'phase.changed', 'task.changed', 'execution.changed', 'approval.changed', 'workflow.changed']);

  let filter = $state('all');
  const inFilter = (t: TaskSummary) => filter === 'all' || (filter === 'active' ? ['active', 'attention'].includes(status(t.state).tone) : status(t.state).tone === 'warning');
  // The service names the phase whose tasks are listed by its id.
  const tasksPhase = (p: ProjectDetail) => { const ph = p.phases.find((x) => x.id === p.tasks_phase); return ph ? `${ph.code} ${ph.title}` : 'this phase'; };
  const doneStates = ['completed', 'approved', 'done'];
  const phaseLink = (p: ProjectDetail['phases'][number]) => (p.state === 'awaiting_phase_review' ? href(`/phases/${p.id}/review`) : undefined);
  // An approval is decided in the pane of its project that holds what it is about.
  const paneOf = (c?: string) => (c === 'phase_completion' ? 'review' : c === 'workflow' ? 'plan' : '');
  const { status: shellStatus } = shellData();
  const broken = $derived(shellStatus.data?.audit.state === 'broken');
  let deciding = $state<{ id: string; mode: 'approve' | 'reject' } | null>(null);
  const refLink = (p: ProjectDetail, t?: string, id?: string) =>
    t === 'approval' ? `/projects/${p.id}/${paneOf(p.pending_approval_items.find((x) => x.id === id)?.category)}`.replace(/\/$/, '') : t === 'execution' ? `/executions/${id}` : '/activity';
</script>

<View {res} label="this project">
  {#snippet children(p)}
    {@const others = p.pending_approval_items}
    {@const curIdx = p.phases.findIndex((x) => !doneStates.includes(x.state))}
    <ProjectHead id={p.id} current="overview" project={p} />
    {#if p.phases.length}
      <Panel title="Phases">
        {#snippet right()}<span class="muted">{Math.max(curIdx, 0) + 1 > p.phases.length ? p.phases.length : Math.max(curIdx, 0) + 1} of {p.phases.length}</span>{/snippet}
        <ol class="stepper" aria-label="Phases">
          {#each p.phases as ph, i}
            {#if i}<li class="ln" class:done={i <= (curIdx < 0 ? p.phases.length : curIdx)} aria-hidden="true"></li>{/if}
            <li class:done={curIdx < 0 || i < curIdx} class:cur={i === curIdx}>
              <span class="c">{#if curIdx < 0 || i < curIdx}<Icon name="check" size={12} />{:else}{i + 1}{/if}</span>
              {#if phaseLink(ph)}<a href={phaseLink(ph)}>{ph.code} {ph.title}</a>{:else}<span>{ph.code} {ph.title}</span>{/if}
              {#if !(curIdx < 0 || i < curIdx)}<Badge state={ph.state} />{/if}
            </li>
          {/each}
        </ol>
      </Panel>
    {:else}
      <Note kind="neu">This project has no workflow yet. Open the <b>Plan</b> tab to ask a harness for a proposal.</Note>
    {/if}
      <div class="quad">
        {#if p.next_step}
          <Panel title="Next step" cls="next-card">
            <div class="pb stack">
              {#if p.pending_approval_items[0]}<div><Badge state="awaiting_approval" text={p.pending_approval_items[0].category_label} /></div>{/if}
              <a class="tlink max-w-[65ch]" href={href(refLink(p, p.next_step.ref_type, p.next_step.ref_id))}>{p.next_step.text}</a>
            </div>
          </Panel>
        {/if}
        <Panel title="Harness">
          <div class="pb stack">
            {#if p.harness}
              <div class="row"><Badge state={p.harness.connected ? 'connected' : 'not_connected'} /><b>{p.harness.name} {p.harness.version ?? ''}</b></div>
              <div class="muted small">{p.harness.credential_active ? 'MCP project credential active.' : 'No credential.'} Guard policy: <b>{p.harness.guard_policy}</b>, {p.harness.would_deny_24h} would-deny in 24 h.</div>
              <div><a class="tlink" href={href('/harnesses')}>Harnesses</a></div>
            {:else}
              <div class="muted">No harness is connected to this project.</div>
              <div><a class="tlink" href={href('/harnesses/connect/' + p.id)}>Connect a harness</a></div>
            {/if}
          </div>
        </Panel>
        {#if others.length}
          <Panel title="Pending approvals">
            <div class="pb stack">
              {#each others as ap (ap.id)}
                <div class="arow">
                  <Badge state="awaiting_approval" text={ap.category_label} />
                  {#if paneOf(ap.category)}<a class="tlink sub" href={href(`/projects/${p.id}/${paneOf(ap.category)}`)}>{visible(ap.subject)}</a>{:else}<span class="sub">{visible(ap.subject)}</span>{/if}
                  <span class="small muted">{ago(ap.requested_at)}</span>
                  {#if !paneOf(ap.category)}
                    <span class="acts"><IconBtn icon="lock" lock kind="pri" label="Approve" context={ap.subject} disabled={broken} onclick={() => (deciding = { id: ap.id, mode: 'approve' })} /><IconBtn icon="x" kind="dng" label="Reject" context={ap.subject} disabled={broken} onclick={() => (deciding = { id: ap.id, mode: 'reject' })} /></span>
                  {/if}
                </div>
              {/each}
            </div>
          </Panel>
        {/if}
        <Panel title="Requirements">
          <div class="pb stack">
            {#each p.requirements as r}
              <div class="row wrap"><Badge state={r.state === 'changed' ? 'stale' : 'approved'} text={r.state === 'changed' ? 'Changed since approval' : 'Unchanged'} /><span class="mono">{r.source}</span></div>
            {:else}<div class="muted">No requirements added yet.</div>{/each}
          </div>
        </Panel>
      </div>
      {#if p.tasks.length}
        {@const shown = p.tasks.filter(inFilter)}
        <Panel title="Tasks in {tasksPhase(p)}" count={p.tasks.length}>
          {#snippet right()}
            <Chips label="Filter tasks" bind:value={filter} options={[
              { id: 'all', label: 'All' },
              { id: 'active', label: 'Active' },
              { id: 'blocked', label: 'Needs action' },
            ]} />
          {/snippet}
          <ul class="items" aria-label="Tasks">
            {#each shown as r (r.id)}
              <li class="li" use:flashOnChange={JSON.stringify(r)}>
                <div class="t"><b>{r.code} {r.title}</b><span class="muted small">{r.depends_on.length ? 'after: ' + r.depends_on.join(', ') : 'no dependencies'}{#if r.execution_id} · <a class="mono" href={href('/executions/' + r.execution_id)}>{r.execution_id}</a>{/if}</span></div>
                {#if r.mode === 'read_only'}<span class="tag">read only</span>{/if}
                <Badge state={r.state} />
                <RowMenu label="Actions for {r.code}" items={[...(r.execution_id ? [{ label: 'Open execution', icon: 'exec' as const, run: () => go('/executions/' + r.execution_id) }] : []), { label: 'Copy task id', icon: 'copy' as const, run: () => copyText(r.id) }]} />
              </li>
            {:else}
              <li class="empty">No task matches this filter.</li>
            {/each}
          </ul>
        </Panel>
      {:else}
        <Panel title="Tasks"><div class="empty">No tasks yet. They appear here once a workflow is approved.</div></Panel>
      {/if}
  {/snippet}
</View>

{#if deciding}
  <DecideFlow approvalId={deciding.id} mode={deciding.mode} oncancel={() => (deciding = null)} ondone={() => { deciding = null; void shellStatus.reload(); }} />
{/if}
