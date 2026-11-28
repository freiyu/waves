<script lang="ts">
  import { visible } from '../lib/text';
  import { app } from '../lib/app.svelte';
  import { shellData } from '../lib/shell';
  import { resource } from '../lib/resource.svelte';
  import { href, go } from '../lib/router.svelte';
  import { ago, capital, countWord, duration, shortClock } from '../lib/format';
  import type { ExecutionSummary } from '../api/models';
  import Panel from '../lib/Panel.svelte';
  import Btn from '../lib/Btn.svelte';
  import TopRow from '../lib/TopRow.svelte';
  import Badge from '../lib/Badge.svelte';
  import Chips from '../lib/Chips.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import Timeline from '../lib/Timeline.svelte';
  import type { Column, Filter } from '../lib/table';
  import RowMenu from '../lib/RowMenu.svelte';
  import { copyText } from '../lib/copy';
  import { waitingOf, waitedFor } from '../lib/waiting.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';
  import RetryTask from '../lib/RetryTask.svelte';
  import { GROUPS, groupOf, inLastHour } from '../lib/timeline';

  const a = app();
  const res = resource(() => a.api.executions(), ['execution.changed']);
  const { status } = shellData();
  // The executions the service says still need the human: a failed one is listed only while its task is blocked by it.
  const flagged = $derived(new Set(status.data?.needs_attention.filter((x) => x.ref_type === 'execution').map((x) => x.ref_id)));
  // The time the timeline is drawn for: taken again whenever the list is read again.
  const nowFor = (_read: unknown) => Date.now();
  // A chip whose last execution moved on is gone; its filter goes with it.
  $effect(() => {
    const items = res.data?.items;
    if (items && filter !== 'all' && !items.some((x) => groupOf(x) === filter)) filter = 'all';
  });
  type Target = { id: string; title: string; code: string };
  let retrying = $state<Target | null>(null);
  let releasing = $state<Target | null>(null);
  const short = (r: ExecutionSummary): Target => ({ id: r.id, title: r.task_title, code: r.task_code });
  let filter = $state('all');
  const STOPPED = ['interrupted', 'disconnected'];
  const headline = (running: number, look: number) =>
    `${running ? capital(countWord(running)) + ' running' : 'Nothing is running'}, ${look ? `${countWord(look)} ${look === 1 ? 'needs' : 'need'} a look` : 'nothing needs a look'}.`;
  const NOTE: Record<string, string> = {
    waiting: 'Claude Code stopped and asks you something in its own session. It keeps its task and its lock until you release it.',
    stopped: 'Retry the task, accept the partial result or mark it failed. Nothing reruns on its own.',
    failed: 'Read the log and the diff. The task stays blocked until you retry it from a terminal with ',
    blocked: 'The task is blocked. Open the execution to read what happened.',
  };
  const when = (r: ExecutionSummary) => (r.waiting_since ? `waiting ${waitedFor(r.waiting_since)}` : r.ended_at ? ago(r.ended_at) : r.started_at ? ago(r.started_at) : '');

  const cols: Column<ExecutionSummary>[] = [
    { key: 'task', label: 'Task', text: (r) => visible(r.task_title), sort: (r) => r.task_title },
    { key: 'state', label: 'State', sort: (r) => r.state, cell: st },
    { key: 'id', label: 'Execution', sort: (r) => r.id, cell: eid },
    { key: 'project', label: 'Project', text: (r) => r.project_name, sort: (r) => r.project_name },
    { key: 'harness', label: 'Harness', text: (r) => r.harness },
    { key: 'started', label: 'Started', sort: (r) => r.started_at ?? '', text: (r) => (r.started_at ? shortClock(r.started_at) : 'not started') },
    { key: 'dur', label: 'Duration', sort: (r) => r.duration_s ?? -1, text: (r) => (r.duration_s == null ? '—' : duration(r.duration_s)) },
    { key: 'menu', label: 'More', cell: menu },
  ];
  const filters: Filter<ExecutionSummary>[] = [
    { id: 'project', label: 'Project', pick: (r) => r.project_name },
    { id: 'harness', label: 'Harness', pick: (r) => r.harness },
  ];
</script>

{#snippet eid(r: ExecutionSummary)}<a class="mono" href={href('/executions/' + r.id)}>{r.id}</a>{/snippet}
{#snippet menu(r: ExecutionSummary)}<RowMenu label="Actions for {r.id}" items={[{ label: 'Open execution', icon: 'exec', run: () => go('/executions/' + r.id) }, { label: 'Copy id', icon: 'copy', run: () => copyText(r.id) }]} />{/snippet}
{#snippet st(r: ExecutionSummary)}<Badge state={r.waiting_since ? 'waiting_for_instruction' : r.state} />{/snippet}

<TopRow>{#snippet crumb()}<span class="crumb">Executions</span>{/snippet}</TopRow>
<View {res} label="executions">
  {#snippet children(d)}
    {@const wait = waitingOf(d.items)}
    {@const runs = d.items.filter((x) => (x.state === 'running' || x.state === 'starting') && !wait.has(x.id))}
    {@const waits = d.items.filter((x) => wait.has(x.id))}
    {@const stopped = d.items.filter((x) => STOPPED.includes(x.state))}
    {@const attention = [...waits, ...stopped, ...d.items.filter((x) => flagged.has(x.id) && !wait.has(x.id) && !STOPPED.includes(x.state))]}
    {@const now = nowFor(d)}
    {@const hour = d.items.filter((x) => inLastHour(x, now))}
    {@const older = d.items.filter((x) => !inLastHour(x, now))}
    {@const options = [{ id: 'all', label: 'All', count: d.items.length }, ...GROUPS.map((g) => ({ ...g, count: d.items.filter((x) => groupOf(x) === g.id).length })).filter((g) => g.count)]}
    {@const pass = (x: ExecutionSummary) => filter === 'all' || groupOf(x) === filter}
    <div class="hero lead">
      <h1><span class="sr-only">Executions: </span>{headline(runs.length, attention.length)}</h1>
      <p>Waves never reruns work by itself. Anything stopped waits here for your decision.</p>
    </div>

    {#if attention.length}
      <section class="flex flex-col gap-4">
        <div class="sect"><h2>Needs attention</h2></div>
        <ul class="cardgrid stagger" aria-label="Needs attention">
          {#each attention as r (r.id)}
            {@const kind = wait.has(r.id) ? 'waiting' : STOPPED.includes(r.state) ? 'stopped' : r.state === 'failed' ? 'failed' : 'blocked'}
            <li><article class="panel dcard">
              <div class="flex items-center justify-between gap-3"><Badge state={kind === 'waiting' ? 'waiting_for_instruction' : r.state} /><span class="text-xs text-fg-muted">{when(r)}</span></div>
              <div><h3><a class="tlink" href={href('/executions/' + r.id)}>{visible(r.task_title)}</a></h3><div class="text-[13px] font-bold">{r.task_code} · {r.project_name}</div></div>
              <p class="text-[13px]">{NOTE[kind]}{#if kind === 'failed'}<span class="mono">waves task retry</span>.{/if}</p>
              <div class="mt-auto flex flex-wrap gap-2.5">
                {#if kind === 'waiting'}<Btn kind="info" label="Release {r.task_title}" onclick={() => (releasing = short(r))}>Release</Btn>
                {:else if kind === 'stopped'}<Btn kind="warn" label="Retry {r.task_title}" onclick={() => (retrying = short(r))}>Retry</Btn><Btn kind="ok" label="Resolve {r.task_title}" href={href(`/executions/${r.id}/recovery`)}>Resolve</Btn>
                {:else}<Btn label="Open execution of {r.task_title}" href={href('/executions/' + r.id)}>Open execution</Btn>{/if}
              </div>
            </article></li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if d.items.length}
      <section class="flex flex-col gap-4">
        <div class="sect !items-center">
          <div><h2>The last hour</h2><div class="text-[13px] text-fg-muted">Each bar is one execution, from start to end or to now.</div></div>
          <Chips label="State filter" bind:value={filter} {options} />
        </div>
        <Timeline items={hour.filter(pass)} {now} />
      </section>
      <Panel title="Older and not started" count={older.length}>
        {#if filter !== 'all'}<p class="px-5 pt-3 text-xs text-fg-muted">The state chip above filters this list too.</p>{/if}
        {#if older.length}
          <Table label="Executions" rows={older.filter(pass)} columns={cols} {filters} rowKey={(r) => r.id} search={{ placeholder: 'Search task or id', text: (r) => `${r.id} ${r.task_title} ${r.task_code}` }} initialSort={{ key: 'started', dir: 'desc' }}>
            {#snippet empty()}<b class="text-fg">No execution here matches</b><div class="mt-1">Change the state filter or the search.</div>{/snippet}
          </Table>
        {:else}
          <div class="empty">Every execution started within the last hour.</div>
        {/if}
      </Panel>
    {:else}
      <div class="panel empty"><b class="text-fg">No executions yet</b><div class="mt-1">Executions appear here after a harness claims a task. Approve a phase first, then start Claude Code in the project.</div></div>
    {/if}

    {#if releasing}
      <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void res.reload(); }} />
    {/if}
    {#if retrying}
      <RetryTask target={retrying} oncancel={() => (retrying = null)} ondone={() => { retrying = null; void res.reload(); }} />
    {/if}
  {/snippet}
</View>
