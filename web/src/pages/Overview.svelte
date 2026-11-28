<script lang="ts">
  import { visible } from '../lib/text';
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { shellData } from '../lib/shell';
  import { href } from '../lib/router.svelte';
  import { ago, capital, countWord, plural, shortDuration } from '../lib/format';
  import { waitingOf, waitedFor, firstAndMore } from '../lib/waiting.svelte';
  import { chainOf } from '../lib/flow';
  import { flashOnChange } from '../lib/flash';
  import { hoverBubble } from '../lib/bubble';
  import type { ApprovalSummary, ExecutionSummary } from '../api/models';
  import Btn from '../lib/Btn.svelte';
  import Note from '../lib/Note.svelte';
  import TopRow from '../lib/TopRow.svelte';
  import Badge from '../lib/Badge.svelte';
  import Bar from '../lib/Bar.svelte';
  import Icon from '../lib/Icon.svelte';
  import View from '../lib/View.svelte';
  import PhaseChain from '../lib/PhaseChain.svelte';
  import StatusDot from '../lib/StatusDot.svelte';
  import DecisionCard from '../lib/DecisionCard.svelte';
  import DecideFlow from '../lib/DecideFlow.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';
  import RetryTask from '../lib/RetryTask.svelte';

  const a = app();
  const { status } = shellData();
  const execs = resource(() => a.api.executions(), ['execution.changed']);
  const pending = resource(() => a.api.approvals('pending'), ['approval.changed']);
  // One detail per project: it carries the tasks of every phase.
  const flows = resource(async () => Promise.all((await a.api.projects()).items.map((p) => a.api.project(p.id))), ['project.changed', 'phase.changed', 'task.changed', 'execution.changed', 'workflow.changed']);

  const broken = $derived(status.data?.audit.state === 'broken');
  let deciding = $state<{ id: string; mode: 'approve' | 'reject' } | null>(null);
  let releasing = $state<{ id: string; title: string; code: string } | null>(null);
  let retrying = $state<{ id: string; title: string; code: string } | null>(null);
  const LIVE = ['running', 'starting', 'waiting_approval', 'paused'];
  const STOPPED = ['interrupted', 'disconnected'];
  const NOUN: Record<string, string> = { phase_completion: 'review', workflow: 'workflow', risky_action: 'risky action', phase_start: 'phase start', result: 'result', task_execution: 'task run' };
  // "1 review, 1 workflow": the kinds that wait, in the order they arrived.
  const kinds = (items: ApprovalSummary[]) => {
    const n = new Map<string, number>();
    for (const x of items) n.set(x.category, (n.get(x.category) ?? 0) + 1);
    return [...n].map(([k, c]) => plural(c, NOUN[k] ?? 'decision')).join(', ') || 'none';
  };
  const headline = (n: number, other: number) => (n === 0 ? (other ? 'Nothing waits for a decision.' : 'Nothing needs you.') : n === 1 ? 'One thing needs you.' : `${capital(countWord(n))} things need you.`);
  // What the service lists besides approvals and is not already a card under Live now: failures, blocked tasks, security alerts.
  const others = $derived(status.data?.needs_attention.filter((x) => x.kind !== 'approval') ?? []);
  const TONE: Record<string, string> = { execution_failed: 'failed', execution_disconnected: 'disconnected', task_blocked: 'blocked' };
  const working = (n: number) => (n === 0 ? 'Claude Code is not working on a task right now.' : `Claude Code is working on ${countWord(n)} ${n === 1 ? 'task' : 'tasks'}.`);
  const short = (r: ExecutionSummary) => ({ id: r.id, title: r.task_title, code: r.task_code });
  const toDecisions = () => document.getElementById('decisions')?.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' });
  const LEGEND = [['done', 'Done'], ['running', 'Running'], ['waiting_for_instruction', 'Waiting'], ['waiting_approval', 'Needs decision'], ['blocked', 'Blocked'], ['queued', 'Queued']] as const;
</script>

<TopRow>
  {#snippet crumb()}<span class="crumb">Overview</span>{/snippet}
  {#snippet actions()}<Btn href={href('/projects?register')}>Register project</Btn>{/snippet}
</TopRow>
<View res={execs} label="the overview">
  {#snippet children(e)}
    {@const wait = waitingOf(e.items)}
    {@const running = e.items.filter((x) => (x.state === 'running' || x.state === 'starting') && !wait.has(x.id))}
    {@const waits = e.items.filter((x) => wait.has(x.id))}
    {@const stopped = e.items.filter((x) => STOPPED.includes(x.state))}
    {@const live = [...running, ...e.items.filter((x) => LIVE.includes(x.state) && !running.includes(x) && !wait.has(x.id)), ...waits, ...stopped]}
    {@const items = pending.data?.items ?? []}
    <div class="hero lead">
      <h1><span class="sr-only">Overview: </span>{pending.data && status.data ? headline(items.length, others.length + waits.length + stopped.length) : 'Overview'}</h1>
      <p>{working(running.length)} Nothing moves to the next phase until you approve it with your passphrase.</p>
    </div>

    {#if status.error && !status.data}<Note kind="warn" role="alert">Alerts and blocked work could not be read: {status.error.message} <button type="button" class="linkbtn font-semibold" onclick={() => void status.reload()}>Retry</button></Note>{/if}

    <div class="stripwrap" use:hoverBubble>
    <span class="hoverbubble" aria-hidden="true"></span>
    <div class="strip stagger" role="group" aria-label="Status now">
      <button type="button" data-tone="attention" onclick={toDecisions}><span class="l text-st-attention"><Icon name="lock" size={14} />Needs your decision</span><span class="n">{pending.data ? items.length : '–'}</span><span class="d">{pending.data ? kinds(items) : 'not loaded yet'}</span></button>
      <a data-tone="active" href={href('/executions')}><span class="l text-st-active"><Icon name="run" size={14} />Running now</span><span class="n">{running.length}</span><span class="d">{running.length ? `across ${plural(new Set(running.map((x) => x.project_id)).size, 'project')}` : 'none'}</span></a>
      <a data-tone="waiting" href={href('/executions')}><span class="l text-st-waiting"><Icon name="prompt" size={14} />Waiting for instruction</span><span class="n">{waits.length}</span><span class="d">{firstAndMore(waits)}</span></a>
      <a data-tone="warning" href={href('/executions')}><span class="l text-st-warning"><Icon name="warn" size={14} />Interrupted</span><span class="n">{stopped.length}</span><span class="d">{firstAndMore(stopped)}</span></a>
    </div>
    </div>

    <section class="decisions flex flex-col" id="decisions" aria-label="Needs your decision">
      <div class="sect"><h2>Needs your decision</h2><span class="aside">Phase approvals always ask for your passphrase.</span></div>
      <View res={pending} label="the decisions">
        {#snippet children(p)}
          {#if p.items.length}
            <ul class="cardgrid stagger" aria-label="Needs your decision">
              {#each p.items as x (x.id)}
                <li><DecisionCard item={x} {broken} ondecide={(id, mode) => (deciding = { id, mode })} /></li>
              {/each}
            </ul>
          {:else}
            <div class="panel empty" role="status"><b>Nothing needs you.</b> No approval is waiting.</div>
          {/if}
        {/snippet}
      </View>
    </section>

    {#if others.some((x) => !live.slice(0, 6).some((r) => r.id === x.ref_id))}
      <section class="flex flex-col gap-3" aria-label="Also needs a look">
        <div class="sect"><h2>Also needs a look</h2></div>
        <ul class="panel items" aria-label="Also needs a look">
          {#each others.filter((x) => !live.slice(0, 6).some((r) => r.id === x.ref_id)) as x (x.kind + x.ref_id)}
            <li class="li flex-wrap">
              <Badge state={TONE[x.kind] ?? 'blocked'} text={x.label} />
              <a class="tlink min-w-0 flex-1 max-[520px]:basis-full" href={href(x.ref_type === 'execution' ? '/executions/' + x.ref_id : '/activity')}>{visible(x.subject)}</a>
              {#if x.project_name}<span class="text-xs text-fg-muted">{x.project_name}</span>{/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    <div class="flowgrid">
      <section aria-label="Projects in flow">
        <div class="sect"><h2>Projects in flow</h2>
          <ul class="legend" aria-label="Legend">{#each LEGEND as [s, name]}<li><span aria-hidden="true"><StatusDot state={s} /></span>{name}</li>{/each}</ul>
        </div>
        <View res={flows} label="projects">
          {#snippet children(list)}
            <ul class="stagger" style="list-style:none;padding:0" aria-label="Projects">
              {#each list as p (p.id)}
                <li class="flowrow">
                  <div class="top2">
                    <h3 class="!text-[19px]"><a class="tlink" href={href('/projects/' + p.id)}>{p.name}</a></h3>
                    <span class="mono text-xs text-fg-muted" title={p.path}>{p.path}</span>
                    <span class="pct">{p.progress_pct}%</span>
                  </div>
                  {#if p.phases.length}<PhaseChain phases={chainOf(p, wait)} />{:else}<div class="text-[13px] text-fg-muted">No workflow yet.</div>{/if}
                </li>
              {:else}
                <li class="empty">No project is registered yet.</li>
              {/each}
            </ul>
          {/snippet}
        </View>
      </section>

      <section aria-label="Live now">
        <div class="sect"><h2>Live now</h2><a class="text-[13px] font-bold no-underline" href={href('/executions')}>All executions</a></div>
        {#if live.length}
          <ul class="stagger flex flex-col gap-4" style="list-style:none;padding:0" aria-label="Live now">
            {#each live.slice(0, 6) as r (r.id)}
              {@const since = wait.get(r.id)}
              <li class="panel lcard" use:flashOnChange={JSON.stringify(r)}>
                <Badge state={since ? 'waiting_for_instruction' : r.state} />
                <h3><a class="tlink" href={href('/executions/' + r.id)}>{visible(r.task_title)}</a></h3>
                <div class="meta">
                  {r.task_code} ·
                  {#if since}answer it in Claude Code · waiting {waitedFor(since)}
                  {:else if STOPPED.includes(r.state)}{r.project_name} · nothing reruns on its own{r.ended_at ? ' · ' + ago(r.ended_at) : ''}
                  {:else}{r.project_name}{r.duration_s != null ? ' · ' + shortDuration(r.duration_s) : ''}{/if}
                </div>
                {#if since}
                  <Btn sm kind="info" label="Release {r.task_title}" onclick={() => (releasing = short(r))}>Release</Btn>
                {:else if STOPPED.includes(r.state)}
                  <div class="flex flex-wrap gap-2.5"><Btn sm kind="warn" label="Retry {r.task_title}" onclick={() => (retrying = short(r))}>Retry</Btn><Btn sm kind="ok" label="Resolve {r.task_title}" href={href(`/executions/${r.id}/recovery`)}>Resolve</Btn></div>
                {:else if r.state === 'running' || r.state === 'starting'}
                  <Bar running label="{r.task_title} is running" />
                {/if}
              </li>
            {/each}
          </ul>
          {#if live.length > 6}<a class="text-[13px] font-semibold" href={href('/executions')}>{live.length - 6} more on the Executions page</a>{/if}
        {:else}
          <div class="panel empty">No execution is active. They appear here after a harness claims a task.</div>
        {/if}
      </section>
    </div>
  {/snippet}
</View>

{#if deciding}
  <DecideFlow approvalId={deciding.id} mode={deciding.mode} oncancel={() => (deciding = null)} ondone={() => { deciding = null; void pending.reload(); void status.reload(); }} />
{/if}
{#if releasing}
  <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void execs.reload(); }} />
{/if}
{#if retrying}
  <RetryTask target={retrying} oncancel={() => (retrying = null)} ondone={() => { retrying = null; void execs.reload(); }} />
{/if}
