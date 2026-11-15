<script lang="ts">
  import { app } from '../lib/app.svelte';
  import { resource } from '../lib/resource.svelte';
  import { href, go } from '../lib/router.svelte';
  import { duration, shortClock, plural } from '../lib/format';
  import { label } from '../lib/status';
  import type { ExecutionSummary } from '../api/models';
  import PageHead from '../lib/PageHead.svelte';
  import Panel from '../lib/Panel.svelte';
  import Note from '../lib/Note.svelte';
  import Badge from '../lib/Badge.svelte';
  import Table from '../lib/Table.svelte';
  import View from '../lib/View.svelte';
  import type { Column, Filter } from '../lib/table';
  import RowMenu from '../lib/RowMenu.svelte';
  import { copyText } from '../lib/copy';
  import Icon from '../lib/Icon.svelte';
  import IconBtn from '../lib/IconBtn.svelte';
  import Confirm from '../lib/Confirm.svelte';
  import { toast } from 'svelte-sonner';
  import { waitingOf, waitedFor, firstAndMore } from '../lib/waiting.svelte';
  import ReleaseLease from '../lib/ReleaseLease.svelte';

  const a = app();
  const res = resource(() => a.api.executions(), ['execution.changed']);
  let ask = $state<null | { kind: 'retry'; r: ExecutionSummary }>(null);
  let releasing = $state<null | { id: string; title: string; code: string }>(null);
  const COPY = {
    retry: { title: 'Retry task', confirm: 'Retry', body: 'The task goes back to Ready for a harness to claim. The interrupted execution stays in the log. Waves never reruns it on its own.' },
  } as const;
  async function run() {
    const { r } = ask!;
    await a.api.resolve(r.id, 'retry_fresh');
    ask = null;
    toast.success('Retry requested', { description: `${r.task_title} (${r.task_code}). Waves never reruns a task on its own.` });
    void res.reload();
  }
  const cols: Column<ExecutionSummary>[] = [
    { key: 'task', label: 'Task', text: (r) => r.task_title, sort: (r) => r.task_title },
    { key: 'state', label: 'State', sort: (r) => r.state, cell: st },
    { key: 'id', label: 'Execution', sort: (r) => r.id, cell: eid },
    { key: 'project', label: 'Project', text: (r) => r.project_name, sort: (r) => r.project_name },
    { key: 'harness', label: 'Harness', text: (r) => r.harness },
    { key: 'started', label: 'Started', sort: (r) => r.started_at ?? '', text: (r) => (r.started_at ? shortClock(r.started_at) : 'none') },
    { key: 'dur', label: 'Duration', sort: (r) => r.duration_s ?? -1, text: (r) => duration(r.duration_s) },
    { key: 'menu', label: 'More', cell: menu },
  ];
  const filters: Filter<ExecutionSummary>[] = [
    { id: 'state', label: 'State', chips: true, row: true, pick: (r) => (r.waiting_since ? 'waiting_for_instruction' : r.state), options: ['running', 'waiting_for_instruction', 'waiting_approval', 'disconnected', 'interrupted', 'failed', 'queued', 'completed'].map((v) => ({ value: v, label: label(v) })) },
    { id: 'project', label: 'Project', pick: (r) => r.project_name },
    { id: 'harness', label: 'Harness', pick: (r) => r.harness },
  ];
</script>

{#snippet eid(r: ExecutionSummary)}<a class="mono" href={href('/executions/' + r.id)}>{r.id}</a>{/snippet}
{#snippet menu(r: ExecutionSummary)}<RowMenu label="Actions for {r.id}" items={[{ label: 'Open execution', icon: 'exec', run: () => go('/executions/' + r.id) }, { label: 'Copy id', icon: 'copy', run: () => copyText(r.id) }]} />{/snippet}
{#snippet st(r: ExecutionSummary)}{#if r.waiting_since}<Badge state="waiting_for_instruction" />{:else}<Badge state={r.state} text={r.state_detail ? `${label(r.state)} · ${r.state_detail}` : undefined} />{/if}{/snippet}

<PageHead title="Executions" />
<View {res} label="executions">
  {#snippet children(d)}
    {@const wait = waitingOf(d.items)}
    {@const runs = d.items.filter((x) => (x.state === 'running' || x.state === 'starting') && !wait.has(x.id))}
    {@const waits = d.items.filter((x) => wait.has(x.id))}
    {@const stopped = d.items.filter((x) => x.state === 'interrupted' || x.state === 'disconnected')}
    {@const attention = [...stopped, ...waits, ...d.items.filter((x) => x.state === 'failed')]}
    <div class="stats three stagger">
      <div class="stat"><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-active-soft text-st-active"><Icon name="run" size={14} /></span>Running now</div><div class="n">{runs.length}</div><div class="muted">{runs.length ? `across ${plural(new Set(runs.map((x) => x.project_id)).size, 'project')}` : 'none'}</div></div>
      <div class="stat"><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-waiting-soft text-st-waiting"><Icon name="prompt" size={14} /></span>Waiting for instruction</div><div class="n">{waits.length}</div><div class="muted">{firstAndMore(waits)}</div></div>
      <div class="stat"><div class="l"><span class="inline-flex size-6 items-center justify-center rounded-md bg-st-warning-soft text-st-warning"><Icon name="warn" size={14} /></span>Interrupted</div><div class="n">{stopped.length}</div><div class="muted">{firstAndMore(stopped)}</div></div>
    </div>
    {#if attention.length}
      <div class="max-w-[774px]"><Panel title="Needs attention" count={attention.length}>
        <div class="grid-head nrow" aria-hidden="true"><span>Task</span><span>Status</span><span></span></div>
        <ul class="items stagger" aria-label="Needs attention">
          {#each attention as r (r.id)}
            <li class="nrow">
              <div class="min-w-0"><a class="tlink" href={href('/executions/' + r.id)}>{r.task_title}</a><div class="muted small truncate">{r.project_name} · {wait.has(r.id) ? `waiting ${waitedFor(wait.get(r.id)!)}` : `${label(r.state).toLowerCase()}${r.started_at ? ' ' + shortClock(r.started_at) : ''}`}</div></div>
              <div><Badge state={wait.has(r.id) ? 'waiting_for_instruction' : r.state} /></div>
              <div class="acts">
                {#if wait.has(r.id)}<IconBtn icon="release" label="Release" context={r.task_title} onclick={() => (releasing = { id: r.id, title: r.task_title, code: r.task_code })} />
                {:else if r.state === 'interrupted' || r.state === 'disconnected'}<IconBtn icon="retry" label="Retry" context={r.task_title} onclick={() => (ask = { kind: 'retry', r })} /><IconBtn icon="arrow" label="Resolve" context={r.task_title} href={href(`/executions/${r.id}/recovery`)} />
                {:else}<IconBtn icon="arrow" label="Recover" context={r.task_title} href={href(`/executions/${r.id}/recovery`)} />{/if}
              </div>
            </li>
          {/each}
        </ul>
      </Panel></div>
    {/if}
    <Panel title="All executions">
      <Table label="Executions" rows={d.items} columns={cols} {filters} rowKey={(r) => r.id} search={{ placeholder: 'Search task or id', text: (r) => `${r.id} ${r.task_title} ${r.task_code}` }} initialSort={{ key: 'started', dir: 'desc' }}>
        {#snippet empty()}
          <b class="text-fg">No executions yet</b>
          <div class="mt-1">Executions appear here after a harness claims a task. Approve a phase first, then start Claude Code in the project.</div>
        {/snippet}
      </Table>
    </Panel>
    {#if releasing}
      <ReleaseLease target={releasing} oncancel={() => (releasing = null)} ondone={() => { releasing = null; void res.reload(); }} />
    {/if}
    {#if ask}
      <Confirm title="{COPY[ask.kind].title} {ask.r.task_code}?" confirm={COPY[ask.kind].confirm} oncancel={() => (ask = null)} onconfirm={run}><p>{COPY[ask.kind].body}</p></Confirm>
    {/if}
    {@const queued = d.items.filter((x) => x.state === 'queued')}
    {#if queued.length}
      <Note kind="neu"><b>{queued.length} queued.</b> {queued.length === 1 ? 'Open it to see why it has not started:' : 'Open any of them to see why they have not started:'} {#each queued.slice(0, 3) as q, i}{#if i}, {/if}<a class="mono" href={href('/executions/' + q.id)}>{q.id}</a>{/each}{queued.length > 3 ? ` and ${queued.length - 3} more` : ''}.</Note>
    {/if}
  {/snippet}
</View>
