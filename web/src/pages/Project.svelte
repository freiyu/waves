<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { route, href, go } from '../lib/router.svelte';
  import { ago, shortDuration, plural } from '../lib/format';
  import type { TaskSummary, ProjectDetail } from '../api/models';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Badge from '../lib/Badge.svelte';
  import Bar from '../lib/Bar.svelte';
  import View from '../lib/View.svelte';
  import Chips from '../lib/Chips.svelte';
  import Journey from '../lib/Journey.svelte';
  import { clipped } from '../lib/clipped';
  import { flashOnChange } from '../lib/flash';
  import { visible } from '../lib/text';
  import DecisionCard from '../lib/DecisionCard.svelte';
  import DecideFlow from '../lib/DecideFlow.svelte';
  import { shellData } from '../lib/shell';
  import ProjectHead from '../lib/ProjectHead.svelte';
  import { waitingOf } from '../lib/waiting.svelte';
  import { taskState } from '../lib/flow';
  import { status, label } from '../lib/status';
  import RowMenu from '../lib/RowMenu.svelte';
  import { copyText } from '../lib/copy';

  const a = app();
  const id = $derived(route.params.id!);
  const res = resource(() => a.api.project(id), ['project.changed', 'phase.changed', 'task.changed', 'execution.changed', 'approval.changed', 'workflow.changed']);
  const execs = resource(() => a.api.executions(), ['execution.changed']);
  const wf = resource(() => a.api.workflow(id), ['workflow.changed']);
  // The approved version holds what the task list does not: acceptance criteria, the objective, the validator commands.
  const current = resource(async () => (wf.data?.current_id ? a.api.workflowVersion(wf.data.current_id) : null), ['workflow.changed']);

  let filter = $state('all');
  const inFilter = (t: TaskSummary, f = filter) => f === 'all' || (f === 'active' ? ['active', 'attention'].includes(status(t.state).tone) : status(t.state).tone === 'warning');
  // The service names the phase whose tasks are listed by its id.
  const phaseOf = (p: ProjectDetail) => p.phases.find((x) => x.id === p.tasks_phase);
  const { status: shellStatus } = shellData();
  const broken = $derived(shellStatus.data?.audit.state === 'broken');
  let deciding = $state<{ id: string; mode: 'approve' | 'reject' } | null>(null);
  const paneOf = (c?: string) => (c === 'phase_completion' ? 'review' : c === 'workflow' ? 'plan' : '');
  const refLink = (p: ProjectDetail, t?: string, ref?: string) =>
    t === 'approval' ? `/projects/${p.id}/${paneOf(p.pending_approval_items.find((x) => x.id === ref)?.category)}`.replace(/\/$/, '') : t === 'execution' ? `/executions/${ref}` : '/activity';
  const short = (h: string) => (h.length > 14 ? `${h.slice(0, 5)}…${h.slice(-4)}` : h);
  const REQUEST: Record<string, string> = { waiting: 'Planning request waiting for a harness', fetched: 'Planning request fetched by a harness', submitted: 'Planning request answered with a proposal' };
</script>

<View {res} label="this project">
  {#snippet children(p)}
    {@const wait = waitingOf(execs.data?.items ?? [])}
    {@const phase = phaseOf(p)}
    {@const spec = current.data?.phases.find((x) => x.id === phase?.id || x.code === phase?.code)}
    <ProjectHead id={p.id} current="overview" project={p} />
    {#if p.phases.length}<Journey project={p} />{:else}<Note kind="neu">This project has no workflow yet. Open the <b>Plan</b> tab to ask a harness for a proposal.</Note>{/if}

    <div class="projcols">
      <div>
        {#if p.tasks.length}
          {@const shown = p.tasks.filter((t) => inFilter(t))}
          <section class="flex flex-col gap-4" aria-label="Tasks in {phase ? `${phase.code} ${phase.title}` : 'this phase'}">
            <div class="sect !items-center">
              <h2>{phase ? `${phase.code} ${visible(phase.title)}` : 'Tasks'}</h2>
              <Chips label="Filter tasks" bind:value={filter} options={[['all', 'All'], ['active', 'Active'], ['blocked', 'Needs action']].map(([id, label]) => ({ id: id!, label: label!, count: p.tasks.filter((t) => inFilter(t, id)).length }))} />
            </div>
            <p class="max-w-[620px] text-fg-muted">{spec?.objective ? visible(spec.objective) + ' ' : ''}Tasks run one after another.</p>
            <ul class="taskcards stagger" aria-label="Tasks">
              {#each shown as t (t.id)}
                {@const e = execs.data?.items.find((x) => x.id === t.execution_id)}
                {@const state = taskState(t, wait)}
                {@const criteria = spec?.tasks.find((x) => x.code === t.code)?.acceptance.length}
                {@const meta = [t.mode === 'read_only' ? 'read only' : 'code change', criteria ? `${criteria} ${criteria === 1 ? 'criterion' : 'criteria'}` : '', t.depends_on.length ? 'after ' + t.depends_on.join(', ') : '', e?.duration_s != null ? shortDuration(e.duration_s) : ''].filter(Boolean).join(' · ')}
                <li class="panel" use:flashOnChange={JSON.stringify([t, state])}>
                  <div class="flex items-center justify-between gap-2"><span class="mono !text-xs text-fg-muted">{t.code}</span><span class="flex items-center gap-1"><Badge {state} /><RowMenu label="Actions for {t.code}" items={[...(t.execution_id ? [{ label: 'Open execution', icon: 'exec' as const, run: () => go('/executions/' + t.execution_id) }] : []), { label: 'Copy task id', icon: 'copy' as const, run: () => copyText(t.id) }]} /></span></div>
                  <div class="ttl" use:clipped={t.title + (t.execution_id ?? '')}>
                    <h3>{#if t.execution_id}<a class="tlink" href={href('/executions/' + t.execution_id)}>{visible(t.title)}</a>{:else}{visible(t.title)}{/if}</h3>
                    <span class="full" aria-hidden="true">{visible(t.title)}</span>
                  </div>
                  <div class="barslot">{#if state === 'in_progress'}<Bar running label="{t.title} is running" />{/if}</div>
                  <div class="truncate text-xs text-fg-muted" title={meta}>{meta}</div>
                </li>
              {:else}
                <li class="panel empty">No task matches this filter.</li>
              {/each}
            </ul>
          </section>
        {:else}
          <div class="panel empty">No tasks yet. They appear here once a workflow is approved.</div>
        {/if}

        {#if spec?.validators.length}
          <section class="flex flex-col gap-4" aria-label="Validators">
            <div class="sect"><h2>Validators</h2></div>
            <div class="finding text-[13px]">Waves runs these commands on your machine when the last task of the phase is done, before you review it.{#if p.pending_approval_items.some((x) => x.category === 'workflow')} A waiting version can change them: read its commands before you approve it.{/if}</div>
            {#each spec.validators as v (v.id)}
              <div class="cli !whitespace-normal">
                <div class="text-xs font-bold" style="font-family: var(--sans)">{visible(v.name)} · {v.kind}{v.kind === 'command' ? (v.timeout_s ? ` · timeout ${v.timeout_s} s` : '') : ' · you confirm'}</div>
                <div class="mt-2 whitespace-pre-wrap">{visible(v.command ?? v.items?.join(' · ') ?? '')}</div>
              </div>
            {/each}
          </section>
        {/if}
      </div>

      <div>
        {#if p.pending_approval_items.length}
          <section aria-label="Pending approvals">
            <ul class="stagger flex flex-col gap-4" style="list-style:none;padding:0">
              {#each p.pending_approval_items as x (x.id)}<li><DecisionCard item={x} inProject {broken} ondecide={(aid, mode) => (deciding = { id: aid, mode })} /></li>{/each}
            </ul>
          </section>
        {/if}
        {#if p.next_step}
          <Panel title="Next step" cls="quiet">
            <div class="pb"><a class="tlink max-w-[65ch]" href={href(refLink(p, p.next_step.ref_type, p.next_step.ref_id))}>{visible(p.next_step.text)}</a></div>
          </Panel>
        {/if}
        <Panel title="Requirements" cls="quiet">
          <div class="pb stack">
            {#each p.requirements as r}
              <div class="flex flex-col items-start gap-1"><span class={r.source.startsWith('Inline:') ? 'text-[13px]' : 'mono'}>{visible(r.source)}</span><Badge state={r.state === 'changed' ? 'stale' : 'approved'} text={r.state === 'changed' ? 'Changed since approval' : 'Unchanged'} /></div>
            {:else}<div class="muted">No requirements added yet.</div>{/each}
            {#if p.planning.request}<div class="muted small">{REQUEST[p.planning.request.state]}, created {ago(p.planning.request.created_at)}.</div>{/if}
          </div>
        </Panel>
        {#if wf.data?.versions.length}
          <Panel title="Versions" cls="quiet">
            <ul class="items" aria-label="Versions">
              {#each wf.data.versions as v (v.id)}
                <li class="li">
                  <span class="flex-1"><a class="tlink" href={href(`/projects/${p.id}/plan`)}>Version {v.number}</a></span>
                  <Badge state={v.state} text={v.id === wf.data.current_id && v.state === 'approved' ? 'In use' : label(v.state)} />
                  <span class="mono !text-xs text-fg-muted">{short(v.content_hash)}</span>
                </li>
              {/each}
            </ul>
          </Panel>
        {/if}
        <Panel title="Harness" cls="quiet">
          <div class="pb stack">
            {#if p.harness}
              <div class="row"><Badge state={p.harness.connected ? 'connected' : 'not_connected'} /><b>{p.harness.name} {p.harness.version ?? ''}</b></div>
              <div class="muted small">{p.harness.credential_active ? 'MCP project credential active.' : 'No credential.'} Guard policy: <b>{p.harness.guard_policy}</b>, {plural(p.harness.would_deny_24h, 'would-deny', 'would-deny')} in 24 h.</div>
              <div><a href={href('/harnesses')}>Harnesses</a></div>
            {:else}
              <div class="muted">No harness is connected to this project.</div>
              <div><a href={href('/harnesses/connect/' + p.id)}>Connect a harness</a></div>
            {/if}
          </div>
        </Panel>
      </div>
    </div>
  {/snippet}
</View>

{#if deciding}
  <DecideFlow approvalId={deciding.id} mode={deciding.mode} oncancel={() => (deciding = null)} ondone={() => { deciding = null; void shellStatus.reload(); void res.reload(); }} />
{/if}
